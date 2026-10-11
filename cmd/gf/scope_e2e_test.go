package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// Write scopes are checked at submit time against git's view of the working
// tree, however the files were written: here with plain file writes, as
// `sed -i` or `cat >` from an agent's Bash would.

func (r *repo) write(name, content string) {
	r.t.Helper()
	path := filepath.Join(r.dir, filepath.FromSlash(name))
	require.NoError(r.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(r.t, os.WriteFile(path, []byte(content), 0o644))
}

// submitAsAgent runs gf submit the way a stage agent does.
func (r *repo) submitAsAgent(args ...string) string {
	r.t.Helper()
	res := r.runWithoutTerminal(asAgent, append([]string{"submit"}, args...)...)
	require.Equalf(r.t, 0, res.code, "gf submit failed: %s", res.stderr)
	return res.stdout
}

func (r *repo) onSpecify() {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.workOnStage()
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.expect("specify", progress.AssignmentOpen, 0, 3)
}

const specifyOutOfScope = "Changed files outside the 'specify' write scope: server/server.go."

func TestScope_SpecifyChangingCode_FailsTheAttempt(t *testing.T) {
	r := newRepo(t)
	r.onSpecify()

	r.workOnStage()
	r.write("server/server.go", fixedServer) // fixed outside the write scope
	out := r.submitAsAgent("--passed")

	assert.Contains(t, out, specifyOutOfScope)
	v := r.expect("specify", progress.AssignmentOpen, 1, 3)
	assert.Equal(t, progress.ProblemFailedCheck, v.Current.LastProblem.Kind)
	assert.True(t, strings.HasPrefix(v.Current.LastProblem.Text, specifyOutOfScope+" specs-fail: no test written in this stage fails (TestPort_Is8080 passed)"),
		v.Current.LastProblem.Text)

	// Committing the change hides nothing: the baseline is the stage visit's start.
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "sneak it in")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, specifyOutOfScope)
	r.expect("specify", progress.AssignmentOpen, 2, 3)

	// Reverting it, with only tests changed, passes.
	r.git("checkout", "-q", "HEAD~1", "--", "server/server.go")
	r.submitAsAgent("--passed")
	r.expect("specify", progress.AssignmentAwaitingApproval, 2, 3)
}

func TestScope_SpecifyWritingOnlyTests_Passes(t *testing.T) {
	r := newRepo(t)
	r.onSpecify()

	r.workOnStage()
	r.write("server/testdata/case.json", "{}\n")
	r.submitAsAgent("--passed")

	r.expect("specify", progress.AssignmentAwaitingApproval, 0, 3)
}

func TestScope_ImplementWritingIntoGofast_FailsTheAttempt(t *testing.T) {
	r := newRepo(t)
	r.onSpecify()
	r.workOnStage()
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.expect("implement", progress.AssignmentOpen, 0, 3)

	r.workOnStage()
	r.write(".gofast/works/other/notes.md", "not this work's\n")
	out := r.submitAsAgent("--passed")

	assert.Contains(t, out, "Changed files outside the 'implement' write scope: .gofast/works/other/notes.md.")
	r.expect("implement", progress.AssignmentOpen, 1, 3)
}

// However an agent changes the NFRs (here, as Bash would, behind the guard's
// back), gf submit finds the change outside the write scope.
func TestScope_ChangingTheNFRs_FailsTheAttempt(t *testing.T) {
	r := newRepo(t)
	r.write(".gofast/nfrs.json", `{"nfrs": [{"id": "port", "areas": ["server/**"], "operation": "GET /", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "10ms"}}]}`)
	r.onSpecify()
	r.workOnStage()
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.expect("implement", progress.AssignmentOpen, 0, 3)

	r.workOnStage()
	r.write(".gofast/nfrs.json", `{"nfrs": [{"id": "port", "areas": ["server/**"], "operation": "GET /", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "10s"}}]}`)
	out := r.submitAsAgent("--passed")

	assert.Contains(t, out, "Changed files outside the 'implement' write scope: .gofast/nfrs.json.")
	r.expect("implement", progress.AssignmentOpen, 1, 3)
}

func TestScope_WithoutBaseline_TheCheckStartsNow(t *testing.T) {
	r := newRepo(t)
	r.onSpecify()
	require.NoError(t, os.RemoveAll(filepath.Join(r.dir, ".gofast", "runtime", "baselines")))
	r.write("server/notes.md", "changed before gf knew the baseline\n")

	out := r.hook(nil, "subagent-start", subagentEvent("SubagentStart", "gofast:specify", false))
	assert.Equal(t, tell("gofast had no record of the working tree when 'specify' began, so its write-scope check starts now."), out)
	assert.Empty(t, r.hook(nil, "subagent-start", subagentEvent("SubagentStart", "gofast:specify", false)), "only once")

	// Without a baseline nothing the visit did can be checked, so a pass fails closed.
	require.NoError(t, os.RemoveAll(filepath.Join(r.dir, ".gofast", "runtime", "baselines")))
	r.workOnStage()
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "Note: no baseline of the working tree was recorded when 'specify' began, so its write-scope check starts now.")
	missing := "The baseline for 'specify' is missing (the work was started before this version of gf, or on another machine). " +
		"The owner should abandon this work with `gf abandon \"<reason>\"` and start it again"
	assert.Contains(t, out, "artifact-written: "+missing)
	assert.Contains(t, out, "specs-fail: "+missing)
	r.expect("specify", progress.AssignmentOpen, 1, 3)
}
