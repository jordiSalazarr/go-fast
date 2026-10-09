package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// The event log is tamper-evident end to end: a change made outside gf blocks
// write commands until the owner accepts it; git's own changes don't.

const logChanged = "The event log was changed outside gf since its last write. Inspect .gofast/events.jsonl (git diff) and run `gf log accept` from your own terminal if the change is legitimate.\n"

func TestSeal_ALineAppendedByHand_BlocksWritesUntilTheOwnerAccepts(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.expect("discovery", progress.AssignmentOpen, 0, 3)

	// An agent submits by writing the event itself, as gf would have.
	opened := map[string]any{}
	require.NoError(t, json.Unmarshal(lastLine(r.readLog()), &opened))
	forged, err := json.Marshal(map[string]any{
		"id": "forged", "position": opened["position"].(float64) + 1, "stream": opened["stream"], "version": 2,
		"type": "SubmittedForApproval", "schema": 1, "occurredAt": opened["occurredAt"],
		"actor":   map[string]any{"kind": "agent", "name": "agent"},
		"payload": map[string]any{"assignmentId": opened["payload"].(map[string]any)["assignmentId"], "attempt": 1},
	})
	require.NoError(t, err)
	f, err := os.OpenFile(r.eventsFile(), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.Write(append(forged, '\n'))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	res := r.runWithoutTerminal(asAgent, "submit", "--passed")
	assert.Equal(t, 1, res.code)
	assert.Equal(t, logChanged, res.stderr)
	res = r.gfFails(nil, "approve")
	assert.Equal(t, logChanged, res.stderr, "owner commands too")

	// Reading still works, with a warning.
	v := r.expect("discovery", progress.AssignmentAwaitingApproval, 0, 3)
	assert.Equal(t, progress.LogChangedWarning, v.LogWarning)
	assert.Contains(t, r.gf("status"), "Warning: The event log was changed outside gf")

	// Only the owner, from a terminal, accepts it.
	res = r.runWithoutTerminal(nil, "log", "accept")
	assert.Equal(t, "Only the owner can accept the event log, from their own terminal. Run `gf log accept` there.\n", res.stderr)
	assert.Equal(t, "Accepted the event log as it is now (4 events). Run `gf status` to see what happens next.\n", r.gf("log", "accept"))

	assert.Empty(t, r.status().LogWarning)
	r.gf("approve")
	r.expect("specify", progress.AssignmentOpen, 0, 3)
}

func TestSeal_GitCheckoutOfAnotherBranchAndBack_IsNotAChange(t *testing.T) {
	r := newRepo(t)
	r.write("main.go", "package main\n")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	r.git("branch", "b")
	r.gf("start", "--type", "fix-bug", "work on main")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "work on main")

	r.git("checkout", "-q", "b") // the log is not committed on b
	assert.Empty(t, r.status().LogWarning)
	r.gf("start", "--type", "fix-bug", "work on b")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "work on b")

	r.git("checkout", "-q", "main")
	v := r.status()
	assert.Empty(t, v.LogWarning)
	assert.Equal(t, "work on main", v.Work.Description)
	r.submitAsAgent("--passed")

	r.git("stash", "-q")
	r.git("checkout", "-q", "b")
	r.git("checkout", "-q", "main")
	r.git("stash", "pop", "-q")
	assert.Empty(t, r.status().LogWarning, "stash, switch, switch back, pop: the log is gf's again")
	r.expect("discovery", progress.AssignmentAwaitingApproval, 0, 3)
}

func TestSeal_DroppingUncommittedEvents_IsAChange(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "work on main")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "start")
	r.submitAsAgent("--failed", "not yet")

	r.git("checkout", "--", ".gofast/events.jsonl") // the failed attempt disappears

	assert.Equal(t, progress.LogChangedWarning, r.status().LogWarning)
	res := r.runWithoutTerminal(asAgent, "submit", "--passed")
	assert.Equal(t, logChanged, res.stderr)
}

func (r *repo) readLog() []byte {
	r.t.Helper()
	data, err := os.ReadFile(r.eventsFile())
	require.NoError(r.t, err)
	return data
}

func lastLine(data []byte) []byte {
	lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
	return lines[len(lines)-1]
}
