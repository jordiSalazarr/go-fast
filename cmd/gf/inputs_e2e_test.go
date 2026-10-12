package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

const walletsSpec = "# Wallets\n\nA wallet holds a balance. Posting a transaction changes it; a debit never takes it below zero.\n"

// Inputs must be committed files of the repository, so every stage reads
// the same thing.
func TestStart_RefusesInputsThatAreNotCommittedFilesOfTheRepository(t *testing.T) {
	r := newRepo(t)
	r.write("docs/wallets.md", walletsSpec)

	res := r.gfFails(nil, "start", "--type", "new-feature", "Wallets", "--spec", "docs/wallets.md")
	assert.Equal(t, "The spec docs/wallets.md is not committed. Commit it before starting the work, so every stage reads the same thing.\n", res.stderr)

	r.git("add", "-A")
	r.git("commit", "-q", "-m", "spec")
	r.write("docs/wallets.md", walletsSpec+"\nAnd refunds.\n")
	res = r.gfFails(nil, "start", "--type", "new-feature", "Wallets", "--spec", "docs/wallets.md")
	assert.Contains(t, res.stderr, "is not committed.", "modified since the commit")

	res = r.gfFails(nil, "start", "--type", "new-feature", "Wallets", "--event-model", "docs/missing.json")
	assert.Equal(t, "The event-model docs/missing.json is not a file inside the repository.\n", res.stderr)
	res = r.gfFails(nil, "start", "--type", "new-feature", "Wallets", "--spec", filepath.Join(os.TempDir(), "x.md"))
	assert.Contains(t, res.stderr, "is not a file inside the repository.")
	res = r.gfFails(nil, "start", "--type", "new-feature", "Wallets", "--spec", "docs")
	assert.Equal(t, "The spec docs is not a file inside the repository.\n", res.stderr)

	r.git("checkout", "--", "docs/wallets.md")
	res = r.gfFails(nil, "start", "--type", "fix-bug", "Wallets", "--event-model", "docs/wallets.md")
	assert.Equal(t, "Only new-feature and modify-behaviour work can start from an event model (--event-model).\n", res.stderr)

	assert.Nil(t, r.status().Work, "nothing started")
}

// The inputs are recorded with their content's hash: status shows them, and
// warns once one changes.
func TestStart_RecordsTheInputs_AndStatusWarnsWhenOneChanges(t *testing.T) {
	r := newRepo(t)
	r.write("docs/wallets.md", walletsSpec)
	r.write("docs/wallets-model.json", `{"events": [{"name": "TransactionPosted"}]}`)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "inputs")

	out := r.gf("start", "--type", "new-feature", "Wallets", "--spec", "docs/wallets.md", "--event-model", "docs/wallets-model.json")
	assert.Contains(t, out, "Input: spec docs/wallets.md (sha256 "+domain.HashOf([]byte(walletsSpec)).String()[:12]+").\n")
	assert.Contains(t, out, "Current stage: event-modelling.")

	v := r.expect("event-modelling", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []progress.InputView{
		{Kind: "spec", Path: "docs/wallets.md", SHA256: domain.HashOf([]byte(walletsSpec)).String()},
		{Kind: "event-model", Path: "docs/wallets-model.json", SHA256: domain.HashOf([]byte(`{"events": [{"name": "TransactionPosted"}]}`)).String()},
	}, v.Work.Inputs)
	assert.Equal(t, []string{"current", "pending", "pending", "pending", "pending", "pending", "pending", "pending", "pending"}, stageStatuses(v))
	assert.NotContains(t, r.gf("status"), "Warning")

	r.write("docs/wallets.md", walletsSpec+"\nAnd refunds.\n")
	v = r.status()
	require.Len(t, v.Work.Inputs, 2)
	assert.True(t, v.Work.Inputs[0].Changed)
	assert.False(t, v.Work.Inputs[1].Changed)
	assert.Contains(t, r.gf("status"), "Input (spec): docs/wallets.md\n  Warning: its content changed since the work started")

	require.NoError(t, os.Remove(filepath.Join(r.dir, "docs", "wallets-model.json")))
	assert.Contains(t, r.gf("status"), "Input (event-model): docs/wallets-model.json\n  Warning: it no longer exists")
}

// Without an event model, feature work skips event modelling and starts on
// grill.
func TestStart_WithoutAnEventModel_StartsOnGrill(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "modify-behaviour", "The server listens on 8080")
	v := r.expect("grill", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []progress.InputView{}, v.Work.Inputs)
	assert.Equal(t, []string{"skipped", "current", "pending", "pending", "pending", "pending", "pending", "pending", "pending"}, stageStatuses(v))
}
