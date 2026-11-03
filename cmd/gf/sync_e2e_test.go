package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

func TestChecks_ShowsTheSyncConfiguration(t *testing.T) {
	r := newRepo(t)
	assert.Contains(t, r.gf("checks"), "\nSync: not configured (.gofast/sync.json does not exist); the history stays in this repository.\n")

	r.write(".gofast/sync.json", `{"server": "http://history.internal", "project": "wallets", "include": ["events", "instructions"]}`)
	out := r.gf("checks")
	assert.Contains(t, out, "Warning: gf sync has no project secret: run `gf sync secret` and paste the one the history server showed when the project was created (or set $GOFAST_PROJECT_SECRET).\n")
	assert.Contains(t, out, "Warning: the sync server http://history.internal is not https")
	assert.Contains(t, out, "\nSync (.gofast/sync.json): project wallets on http://history.internal\n  sends        events, instructions\n  secret       missing: run `gf sync secret`\n")

	r.write(".gofast/sync.json", `{"server": "https://history.example.com", "project": "wallets"}`)
	res := r.run(map[string]string{"GOFAST_PROJECT_SECRET": "s3cret"}, "checks")
	assert.NotContains(t, res.stdout, "gf sync has no project secret")
	assert.NotContains(t, res.stdout, "is not https")
	assert.Contains(t, res.stdout, "  sends        events, instructions, artifacts, decisions\n  secret       from $GOFAST_PROJECT_SECRET\n")

	r.write(".gofast/sync.json", `{"server": "https://history.example.com", "project": "Wallets"}`)
	assert.Contains(t, r.gfFails(nil, "checks").stderr, "The sync configuration .gofast/sync.json is invalid: \"project\" must be the project's id on the server")
}

func TestSync_IsTheOwnersFromTheirTerminal_AndNeedsAConfigurationAndTheSecret(t *testing.T) {
	r := newRepo(t)
	res := r.runWithoutTerminal(nil, "sync")
	assert.Equal(t, 1, res.code)
	assert.Equal(t, "Only the owner can sync the work history, from their own terminal. Run `gf sync` there.\n", res.stderr)
	res = r.gfFails(asAgent, "sync")
	assert.Equal(t, "Only the owner can sync the work history. Ask the owner to run `gf sync`.\n", res.stderr)

	res = r.gfFails(nil, "sync")
	assert.Equal(t, "Sync is not configured: create .gofast/sync.json with the server's URL and the project's id. `gf checks` shows the result.\n", res.stderr)
	r.write(".gofast/sync.json", `{"server": "https://history.example.com", "project": "wallets"}`)
	res = r.gfFails(nil, "sync", "--dry-run")
	assert.Equal(t, "gf sync has no project secret: run `gf sync secret` and paste the one the history server showed when the project was created, then run `gf sync` again.\n", res.stderr)
	res = r.gfFails(map[string]string{"GOFAST_SYNC_TOKEN": "s3cret"}, "sync", "--dry-run")
	assert.Equal(t, "$GOFAST_SYNC_TOKEN is now $GOFAST_PROJECT_SECRET. Better: run `gf sync secret` and paste the project secret, then run `gf sync` again.\n", res.stderr)
}

func TestSyncSecret_SavesThePastedSecret_WhereGitIgnoresIt(t *testing.T) {
	r := newRepo(t)
	res := r.gfFails(nil, "sync", "secret")
	assert.Contains(t, res.stderr, "Sync is not configured: create .gofast/sync.json")
	r.write(".gofast/sync.json", `{"server": "https://history.example.com", "project": "wallets"}`)
	res = r.runWithoutTerminal(nil, "sync", "secret")
	assert.Equal(t, "Only the owner can save the project secret, from their own terminal. Run `gf sync secret` there.\n", res.stderr)

	res = r.runWithInput(strings.Repeat("ab", 32)+"\n", "sync", "secret")
	assert.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "That isn't a project secret: they begin gfps_")

	secret := "gfps_" + strings.Repeat("cd", 32)
	res = r.runWithInput("  "+secret+"\n", "sync", "secret")
	require.Equal(t, 0, res.code, res.stderr)
	assert.Equal(t, "Paste the secret of project wallets (it begins gfps_), then press Enter: \n"+
		"Saved the secret of project wallets in .gofast/sync.secret, which git ignores. `gf sync` uses it.\n", res.stdout)
	assert.Equal(t, secret+"\n", r.readFile(".gofast/sync.secret"))
	assert.Empty(t, r.git("status", "--porcelain", "--ignored=no", ".gofast/sync.secret"), "git ignores it")
	assert.Contains(t, r.gf("checks"), "  secret       from .gofast/sync.secret\n")
}

func TestStatus_SaysHowFarTheHistoryIsSynced(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	assert.Contains(t, r.gf("status"), "\n\nSync: not configured.\n")
	v := r.status()
	require.NotNil(t, v.Sync)
	assert.False(t, v.Sync.Configured)

	r.write(".gofast/sync.json", `{"server": "https://history.example.com", "project": "wallets"}`)
	pending := len(r.eventTypes())
	assert.Contains(t, r.gf("status"), fmt.Sprintf("\n\nSync: never synced; %d events pending. The owner runs `gf sync` in their own terminal.\n", pending))
	v = r.status()
	assert.Equal(t, &progress.SyncView{Configured: true, Pending: pending}, v.Sync)

	r.write(".gofast/sync.json", `{"server": "ftp://x", "project": "wallets"}`)
	assert.Contains(t, r.gf("status"), "\n\nSync: .gofast/sync.json is invalid. `gf checks` says why.\n", "a bad configuration never stops gf status")
}
