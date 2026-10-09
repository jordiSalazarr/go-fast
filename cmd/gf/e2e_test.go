package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/slices/status"
)

// repo is a temporary git repository driven through the root command.
type repo struct {
	t   *testing.T
	dir string
}

type result struct {
	code   int
	stdout string
	stderr string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "Jordi"},
		{"config", "user.email", "jordi@example.com"},
	} {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
	}
	return &repo{t: t, dir: dir}
}

func (r *repo) run(env map[string]string, args ...string) result {
	r.t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(append([]string{"--dir", r.dir}, args...), func(k string) string { return env[k] }, strings.NewReader(""), &stdout, &stderr)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// gf runs a command as the owner and requires it to succeed.
func (r *repo) gf(args ...string) string {
	r.t.Helper()
	res := r.run(nil, args...)
	require.Equalf(r.t, 0, res.code, "gf %s failed: %s", strings.Join(args, " "), res.stderr)
	return res.stdout
}

// gfFails runs a command and requires it to be rejected.
func (r *repo) gfFails(env map[string]string, args ...string) result {
	r.t.Helper()
	res := r.run(env, args...)
	require.Equalf(r.t, 1, res.code, "gf %s should fail; stdout: %s", strings.Join(args, " "), res.stdout)
	return res
}

func (r *repo) status() status.View {
	r.t.Helper()
	var v status.View
	require.NoError(r.t, json.Unmarshal([]byte(r.gf("status", "--json")), &v))
	return v
}

// expect checks the current stage visit as reported by `gf status --json`.
func (r *repo) expect(stage, assignment string, attemptsUsed, budget int) status.View {
	r.t.Helper()
	v := r.status()
	require.NotNil(r.t, v.Current, "expected active work")
	assert.Equal(r.t, stage, v.Current.Stage, "stage")
	assert.Equal(r.t, assignment, v.Current.Assignment, "assignment state")
	assert.Equal(r.t, attemptsUsed, v.Current.AttemptsUsed, "attempts used")
	assert.Equal(r.t, budget, v.Current.Budget, "budget")
	return v
}

func (r *repo) eventTypes() []string {
	r.t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.dir, ".gofast", "events.jsonl"))
	require.NoError(r.t, err)
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var env struct {
			Type string `json:"type"`
		}
		require.NoError(r.t, json.Unmarshal([]byte(line), &env))
		types = append(types, env.Type)
	}
	return types
}

var asAgent = map[string]string{"GF_ACTOR": "agent"}

func TestFixBugPath_EndToEnd(t *testing.T) {
	r := newRepo(t)

	v := r.status()
	assert.Nil(t, v.Work)
	assert.Contains(t, v.Next.Message, "gf start --type fix-bug")

	r.gf("start", "--type", "fix-bug", "login button does nothing")
	v = r.expect("discovery", status.AssignmentOpen, 0, 3)
	assert.Equal(t, "fix-bug", v.Work.Type)
	assert.Equal(t, "login button does nothing", v.Work.Description)
	assert.Equal(t, status.NextAgent, v.Next.Actor)
	assert.Equal(t, []string{"current", "pending", "pending", "pending", "pending"}, stageStatuses(v))

	// A second start while work is active is rejected, naming the active work.
	res := r.gfFails(nil, "start", "--type", "fix-bug", "another bug")
	assert.Contains(t, res.stderr, "There is already active work")
	assert.Contains(t, res.stderr, "login button does nothing")

	// Discovery: a failed attempt, a rejection, then approval.
	out := r.run(asAgent, "submit", "--failed", "cannot reproduce yet")
	require.Equal(t, 0, out.code, out.stderr)
	v = r.expect("discovery", status.AssignmentOpen, 1, 3)
	assert.Equal(t, &status.ProblemView{Kind: status.ProblemFailedCheck, Attempt: 1, Text: "cannot reproduce yet"}, v.Current.LastProblem)

	r.run(asAgent, "submit", "--passed")
	v = r.expect("discovery", status.AssignmentAwaitingApproval, 1, 3)
	assert.Equal(t, status.NextOwner, v.Next.Actor)
	assert.Contains(t, v.Next.Message, "gf approve")

	// Owner-only commands refuse an agent.
	res = r.gfFails(asAgent, "approve")
	assert.Equal(t, "Only the owner can approve. Ask the owner to run `gf approve`.\n", res.stderr)
	for _, args := range [][]string{{"reject", "x"}, {"extend", "1"}, {"abandon", "x"}} {
		res = r.gfFails(asAgent, args...)
		assert.Contains(t, res.stderr, "Only the owner can")
	}
	r.expect("discovery", status.AssignmentAwaitingApproval, 1, 3)

	r.gf("reject", "add the browser console output")
	v = r.expect("discovery", status.AssignmentOpen, 2, 3)
	assert.Equal(t, &status.ProblemView{Kind: status.ProblemRejection, Attempt: 2, Text: "add the browser console output"}, v.Current.LastProblem)

	r.gf("submit", "--passed")
	r.gf("approve")

	// Specify: straight approval on the second human gate.
	v = r.expect("specify", status.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"done", "current", "pending", "pending", "pending"}, stageStatuses(v))
	r.gf("submit", "--passed")
	r.expect("specify", status.AssignmentAwaitingApproval, 0, 3)
	r.gf("approve")

	// Implement: three failures escalate; the owner extends the budget.
	r.expect("implement", status.AssignmentOpen, 0, 3)
	r.gf("submit", "--failed", "unit tests fail")
	r.gf("submit", "--failed", "unit tests still fail")
	out2 := r.gf("submit", "--failed", "flaky test")
	assert.Contains(t, out2, "Escalated")
	v = r.expect("implement", status.AssignmentEscalated, 3, 3)
	assert.Equal(t, status.NextOwner, v.Next.Actor)
	assert.Contains(t, v.Next.Message, "gf extend")

	res = r.gfFails(nil, "submit", "--passed")
	assert.Contains(t, res.stderr, "nothing to submit")

	r.gf("extend", "2")
	r.expect("implement", status.AssignmentOpen, 3, 5)
	r.gf("submit", "--passed") // auto gate: accepted, work advances

	// Review and integration testing pass on auto gates.
	r.expect("review", status.AssignmentOpen, 0, 3)
	r.gf("submit", "--passed")
	r.expect("integration-testing", status.AssignmentOpen, 0, 3)
	r.gf("submit", "--passed")

	v = r.status()
	assert.Nil(t, v.Work, "work completed")
	types := r.eventTypes()
	assert.Equal(t, "WorkCompleted", types[len(types)-1])
	assert.Equal(t, 1, count(types, "WorkStarted"))
	assert.Equal(t, 5, count(types, "AssignmentOpened"))
	assert.Equal(t, 5, count(types, "AssignmentAccepted"))
	assert.Equal(t, 1, count(types, "AssignmentEscalated"))
	assert.Equal(t, 1, count(types, "BudgetExtended"))
	assert.Equal(t, 1, count(types, "AssignmentRejected"))

	// A new work can start once the previous one is completed.
	r.gf("start", "--type", "fix-bug", "next bug")
	r.expect("discovery", status.AssignmentOpen, 0, 3)
}

func TestAbandon_CancelsTheOpenAssignment(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "crash on save")
	r.gf("submit", "--passed")
	r.expect("discovery", status.AssignmentAwaitingApproval, 0, 3)

	out := r.gf("abandon", "not reproducible")

	assert.Contains(t, out, "Abandoned")
	types := r.eventTypes()
	assert.Equal(t, []string{"WorkAbandoned", "AssignmentCancelled"}, types[len(types)-2:])
	assert.Nil(t, r.status().Work)
	res := r.gfFails(nil, "approve")
	assert.Contains(t, res.stderr, "There is no active work")
}

func TestErrors_AreFriendlyOnTheTerminalAndDetailedInTheLog(t *testing.T) {
	r := newRepo(t)

	res := r.gfFails(nil, "approve")
	assert.Equal(t, "There is no active work on this branch. Start one with `gf start --type fix-bug \"<description>\"`.\n", res.stderr)

	res = r.gfFails(nil, "start", "--type", "feature", "x")
	assert.Equal(t, "Unknown work type. Available: fix-bug.\n", res.stderr)

	res = r.gfFails(nil, "submit", "--passed", "--failed", "x")
	assert.Contains(t, res.stderr, "Choose exactly one of --passed or --failed")
	assert.Contains(t, res.stderr, "gf submit --help")

	res = r.gfFails(nil, "extend", "zero")
	assert.Contains(t, res.stderr, "positive whole number")

	log, err := os.ReadFile(filepath.Join(r.dir, ".gofast", "gf.log"))
	require.NoError(t, err)
	assert.Contains(t, string(log), "approve: branch main: no active work")
	assert.Contains(t, string(log), `work type \"feature\" (available: fix-bug): unknown work type`)
	assert.NotContains(t, res.stderr, "attempt budget must be", "no raw error chain on the terminal")
}

func TestOwnerWithoutGitIdentity_IsToldHowToFixIt(t *testing.T) {
	r := newRepo(t)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	out, err := exec.Command("git", "-C", r.dir, "config", "--unset", "user.name").CombinedOutput()
	require.NoError(t, err, string(out))

	res := r.gfFails(nil, "start", "--type", "fix-bug", "x")

	assert.Contains(t, res.stderr, "git config user.name")
}

func stageStatuses(v status.View) []string {
	var s []string
	for _, st := range v.Work.Stages {
		s = append(s, st.Status)
	}
	return s
}

func count(values []string, v string) int {
	n := 0
	for _, x := range values {
		if x == v {
			n++
		}
	}
	return n
}
