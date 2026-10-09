package main

import (
	"os"
	"path/filepath"
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
	r.write("main.go", "package main\n")
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.expect("specify", progress.AssignmentOpen, 0, 3)
}

func TestScope_SpecifyChangingCode_FailsTheAttempt(t *testing.T) {
	r := newRepo(t)
	r.onSpecify()

	r.write("main.go", "package main // fixed outside the write scope\n")
	r.write("main_test.go", "package main\n")
	out := r.submitAsAgent("--passed")

	assert.Contains(t, out, "Changed files outside the 'specify' write scope: main.go.")
	v := r.expect("specify", progress.AssignmentOpen, 1, 3)
	assert.Equal(t, &progress.ProblemView{Kind: progress.ProblemFailedCheck, Attempt: 1,
		Text: "Changed files outside the 'specify' write scope: main.go."}, v.Current.LastProblem)

	// Committing the change hides nothing: the baseline is the stage visit's start.
	r.git("commit", "-q", "-am", "sneak it in")
	r.submitAsAgent("--passed")
	r.expect("specify", progress.AssignmentOpen, 2, 3)

	// Reverting it, with only tests changed, passes.
	r.git("checkout", "-q", "HEAD~1", "--", "main.go")
	r.submitAsAgent("--passed")
	r.expect("specify", progress.AssignmentAwaitingApproval, 2, 3)
}

func TestScope_SpecifyWritingOnlyTests_Passes(t *testing.T) {
	r := newRepo(t)
	r.onSpecify()

	r.write("main_test.go", "package main\n")
	r.write("testdata/case.json", "{}\n")
	r.write(r.status().Current.Artifact, "# specify\n")
	r.submitAsAgent("--passed")

	r.expect("specify", progress.AssignmentAwaitingApproval, 0, 3)
}

func TestScope_ImplementWritingIntoGofast_FailsTheAttempt(t *testing.T) {
	r := newRepo(t)
	r.onSpecify()
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.expect("implement", progress.AssignmentOpen, 0, 3)

	r.write("main.go", "package main // the fix\n")
	r.write(".gofast/works/other/notes.md", "not this work's\n")
	out := r.submitAsAgent("--passed")

	assert.Contains(t, out, "Changed files outside the 'implement' write scope: .gofast/works/other/notes.md.")
	r.expect("implement", progress.AssignmentOpen, 1, 3)
}

func TestScope_WithoutBaseline_TheCheckStartsNow(t *testing.T) {
	r := newRepo(t)
	r.onSpecify()
	require.NoError(t, os.RemoveAll(filepath.Join(r.dir, ".gofast", "runtime", "baselines")))
	r.write("main.go", "package main // changed before gf knew the baseline\n")

	out := r.hook(nil, "subagent-start", subagentEvent("SubagentStart", "gofast:specify", false))
	assert.Equal(t, tell("gofast had no record of the working tree when 'specify' began, so its write-scope check starts now."), out)
	assert.Empty(t, r.hook(nil, "subagent-start", subagentEvent("SubagentStart", "gofast:specify", false)), "only once")

	require.NoError(t, os.RemoveAll(filepath.Join(r.dir, ".gofast", "runtime", "baselines")))
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "Note: no baseline of the working tree was recorded when 'specify' began, so its write-scope check starts now.")
	r.expect("specify", progress.AssignmentAwaitingApproval, 0, 3)
}
