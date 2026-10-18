package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
