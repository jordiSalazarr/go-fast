package main

import (
	"fmt"
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
	assert.Contains(t, out, "Warning: $GOFAST_SYNC_TOKEN is not set: gf sync needs the project's token. Set it in your own shell, never in a file.\n")
	assert.Contains(t, out, "Warning: the sync server http://history.internal is not https")
	assert.Contains(t, out, "\nSync (.gofast/sync.json): project wallets on http://history.internal\n  sends        events, instructions\n  token        $GOFAST_SYNC_TOKEN missing\n")

	r.write(".gofast/sync.json", `{"server": "https://history.example.com", "project": "wallets"}`)
	res := r.run(map[string]string{"GOFAST_SYNC_TOKEN": "t0ken"}, "checks")
	assert.NotContains(t, res.stdout, "GOFAST_SYNC_TOKEN is not set")
	assert.NotContains(t, res.stdout, "is not https")
	assert.Contains(t, res.stdout, "  sends        events, instructions, artifacts, decisions\n  token        $GOFAST_SYNC_TOKEN set\n")

	r.write(".gofast/sync.json", `{"server": "https://history.example.com", "project": "Wallets"}`)
	assert.Contains(t, r.gfFails(nil, "checks").stderr, "The sync configuration .gofast/sync.json is invalid: \"project\" must be the project's id on the server")
}

func TestSync_IsTheOwnersFromTheirTerminal_AndNeedsAConfigurationAndAToken(t *testing.T) {
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
	assert.Equal(t, "Set $GOFAST_SYNC_TOKEN to the project's token in your own shell, then run `gf sync` again.\n", res.stderr)
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
