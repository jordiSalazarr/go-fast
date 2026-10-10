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

func TestChecks_ShowsTheConfigurationAndEachStagesChecks(t *testing.T) {
	r := newRepo(t)

	out := r.gf("checks")
	assert.Contains(t, out, "defaults; .gofast/checks.json does not exist")
	assert.Contains(t, out, "integration  not configured: integration-passes fails until the owner adds one to .gofast/checks.json")
	assert.Contains(t, out, "  specify              artifact-written, specs-fail\n")
	assert.Contains(t, out, "  implement            artifact-written, build-passes, specs-pass, specs-untouched, suite-passes\n")

	r.write(".gofast/checks.json", `{"integration": {"command": ["go", "test", "-tags=integration", "./..."], "timeout": "15m"}, "timeout": "5m"}`)
	out = r.gf("checks")
	assert.Contains(t, out, "Exit-check configuration (.gofast/checks.json):")
	assert.Contains(t, out, "timeout      5m0s per check command")
	assert.Contains(t, out, "integration  go test -tags=integration ./... (timeout 15m0s)")

	r.write(".gofast/checks.json", `{"timeout": "soon"}`)
	res := r.gfFails(nil, "checks")
	assert.Equal(t, "The exit-check configuration .gofast/checks.json is invalid: \"timeout\" must be a positive Go duration such as \"10m\", not \"soon\". The owner fixes it; `gf checks` shows the result.\n", res.stderr)
}

// A fix-bug run where gf, not the agent, decides whether each stage passes.
func TestExitChecks_FixBugRun_EndToEnd(t *testing.T) {
	r := newRepo(t)
	r.configureChecks()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	r.gf("start", "--type", "fix-bug", "The port 8080 is not listening")

	// Discovery: a pass without an artifact fails.
	v := r.expect("discovery", progress.AssignmentOpen, 0, 3)
	workID := v.Work.ID
	assert.Equal(t, []string{"artifact-written"}, v.Current.Checks)
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ artifact-written: "+v.Current.Artifact+" is missing")
	v = r.expect("discovery", progress.AssignmentOpen, 1, 3)
	assert.Equal(t, &progress.ProblemView{Kind: progress.ProblemFailedCheck, Attempt: 1,
		Text: "artifact-written: " + v.Current.Artifact + " is missing."}, v.Current.LastProblem)
	r.workOnStage()
	r.submitAsAgent("--passed")
	r.gf("approve")

	// Specify: a test that already passes is not a spec of the bug.
	v = r.expect("specify", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "specs-fail"}, v.Current.Checks)
	r.write("server/positive_test.go", "package server\n\nimport \"testing\"\n\nfunc TestPort_IsPositive(t *testing.T) {\n\tif Port() <= 0 {\n\t\tt.Fatal(\"no port\")\n\t}\n}\n")
	r.write(v.Current.Artifact, "# specify\n\nTestPort_IsPositive.\n")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ artifact-written\n✗ specs-fail: no test written in this stage fails (TestPort_IsPositive passed)")
	v = r.expect("specify", progress.AssignmentOpen, 1, 3)
	assert.True(t, strings.HasPrefix(v.Current.LastProblem.Text, "specs-fail: no test written in this stage fails (TestPort_IsPositive passed) (log: .gofast/runtime/checks/"),
		v.Current.LastProblem.Text)
	r.workOnStage() // a test that fails because of the bug
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ specs-fail")
	r.expect("specify", progress.AssignmentAwaitingApproval, 1, 3)
	r.gf("approve")

	// Implement: changing specify's test instead of the code fails.
	v = r.expect("implement", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "build-passes", "specs-pass", "specs-untouched", "suite-passes"}, v.Current.Checks)
	r.write("server/port_test.go", strings.ReplaceAll(portSpec, "8080", "8081"))
	r.write(v.Current.Artifact, "# implement\n\nAdjusted the test.\n")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ specs-untouched: the tests written during specify were changed: server/port_test.go (modified); restore them")
	v = r.expect("implement", progress.AssignmentOpen, 1, 3)
	assert.Contains(t, v.Current.LastProblem.Text, "specs-untouched: the tests written during specify were changed: server/port_test.go (modified)")
	r.write("server/port_test.go", portSpec)
	r.workOnStage() // the fix
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ specs-pass\n✓ specs-untouched\n✓ suite-passes\n'implement' accepted.")

	// Review and integration testing pass on their checks, the latter running the configured command.
	v = r.expect("review", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "formatted", "build-passes", "suite-passes"}, v.Current.Checks)
	r.workOnStage()
	r.submitAsAgent("--passed")
	v = r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "integration-passes"}, v.Current.Checks)
	r.workOnStage()
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ integration-passes")
	assert.Nil(t, r.status().Work, "work completed")

	log, err := os.ReadFile(filepath.Join(r.dir, ".gofast", "runtime", "checks", workID+"-integration-testing-1-attempt1.log"))
	require.NoError(t, err)
	assert.Contains(t, string(log), "$ go test ./...")
}

func TestExitChecks_AFailedClaimRunsNoChecks(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "The port 8080 is not listening")

	out := r.submitAsAgent("--failed", "cannot reproduce")

	assert.NotContains(t, out, "Running the exit checks")
	v := r.expect("discovery", progress.AssignmentOpen, 1, 3)
	assert.Equal(t, "cannot reproduce", v.Current.LastProblem.Text)
}

func TestExitChecks_IntegrationFailsClosedWithoutConfiguration(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "The port 8080 is not listening")
	for _, stage := range []string{"discovery", "specify"} {
		r.expect(stage, progress.AssignmentOpen, 0, 3)
		r.workOnStage()
		r.submitAsAgent("--passed")
		r.gf("approve")
	}
	for range 2 { // implement, review
		r.workOnStage()
		r.submitAsAgent("--passed")
	}
	r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	r.workOnStage()

	out := r.submitAsAgent("--passed")

	assert.Contains(t, out, "✗ integration-passes: No integration command is configured. The owner adds one to .gofast/checks.json")
	r.expect("integration-testing", progress.AssignmentOpen, 1, 3)
}
