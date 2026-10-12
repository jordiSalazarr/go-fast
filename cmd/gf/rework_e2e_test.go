package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// Rework end to end: review and integration testing hand code failures to
// the fixer, which sends the work back to specify or implement.

const (
	// buggyAddr and fixedAddr: a second bug, which fixing Port doesn't fix.
	buggyAddr = "package server\n\n// Addr is the address the server listens on.\nfunc Addr() string { return \":8081\" }\n"
	fixedAddr = "package server\n\n// Addr is the address the server listens on.\nfunc Addr() string { return \":8080\" }\n"
	// addrSpec fails against buggyAddr and passes against fixedAddr.
	addrSpec = "package server\n\nimport \"testing\"\n\nfunc TestAddr_Is8080(t *testing.T) {\n\tif got := Addr(); got != \":8080\" {\n\t\tt.Fatalf(\"Addr() = %q, want :8080\", got)\n\t}\n}\n"
	// addrIntegration is addrSpec run only by the integration command.
	addrIntegration = "//go:build integration\n\n" + addrSpec
)

// newReworkRepo is a repository whose integration command runs the
// integration-tagged tests, with everything committed before work starts.
func newReworkRepo(t *testing.T, integration bool) *repo {
	t.Helper()
	r := newRepo(t)
	r.write("server/addr.go", buggyAddr)
	if integration {
		r.write("server/addr_integration_test.go", strings.Replace(addrIntegration, "TestAddr_Is8080", "TestAddr_IntegrationIs8080", 1))
	}
	r.write(".gofast/checks.json", `{"integration": {"command": ["go", "test", "-tags=integration", "./..."]}, "timeout": "2m"}`)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	return r
}

// toReview starts the work and takes it through discovery, specify (which
// specifies only the Port bug) and implement, to review's first visit.
func (r *repo) toReview() progress.View {
	r.t.Helper()
	r.gf("start", "--type", "fix-bug", "The server does not listen on 8080")
	for range 2 { // discovery, specify
		r.workOnStage()
		r.submitAsAgent("--passed")
		r.gf("approve")
	}
	r.workOnStage() // implement fixes Port
	r.submitAsAgent("--passed")
	return r.expect("review", progress.AssignmentOpen, 0, 3)
}

// writeArtifact writes the current stage visit's artifact.
func (r *repo) writeArtifact(text string) {
	r.t.Helper()
	c := r.status().Current
	require.NotNil(r.t, c)
	r.write(c.Artifact, "# "+c.Stage+"\n\n"+text+"\n")
}

// 1. Integration testing fails because of a code bug; the fixer sends the
// work to implement, whose second visit fixes it.
func TestRework_IntegrationFailsOnACodeBug_TheFixerSendsItToImplement(t *testing.T) {
	r := newReworkRepo(t, true)
	v := r.toReview()
	workID := v.Work.ID
	r.workOnStage()
	r.submitAsAgent(reviewPass...)

	r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	r.workOnStage()
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ integration-passes")
	assert.Contains(t, out, "the work goes to the fixer (no attempt used)")

	// The fixer: what handed over, and where it may send the work.
	v = r.expect("fixer", progress.AssignmentOpen, 0, 3)
	require.NotNil(t, v.Current.Fixer)
	assert.Equal(t, "integration-testing", v.Current.Fixer.From)
	assert.Equal(t, "correctness", v.Current.Fixer.Mode)
	assert.Contains(t, v.Current.Fixer.Reason, "integration-passes")
	assert.Equal(t, ".gofast/works/"+workID+"/integration-testing-v1.md", v.Current.Fixer.Artifact)
	assert.Equal(t, ".gofast/runtime/checks/"+workID+"-integration-testing-1-attempt1.log", v.Current.Fixer.CheckLog)
	assert.Equal(t, []string{"specify", "implement"}, v.Current.Fixer.Targets)
	assert.Equal(t, 1, v.Work.Reworks)
	assert.Contains(t, r.gf("status"), "Handed over by: integration-testing (visit 1), correctness:")
	r.writeArtifact("Root cause: Addr hard-codes 8081. Implement must make Addr return :8080.")
	out = r.submitAsAgent("--passed", "--route", "implement", "Addr hard-codes 8081")
	assert.Contains(t, out, "'fixer' accepted: the work returns to 'implement' (Addr hard-codes 8081).")

	// Implement's second visit sees the fixer's artifact, and fixes Addr.
	v = r.expect("implement", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, ".gofast/works/"+workID+"/implement-v2.md", v.Current.Artifact)
	assert.Equal(t, &progress.ReworkOfView{
		FixerArtifact: ".gofast/works/" + workID + "/fixer-v1.md", Reason: "Addr hard-codes 8081", Target: "implement", TargetVisit: 2,
	}, v.Current.ReworkOf)
	r.write("server/addr.go", fixedAddr)
	r.writeArtifact("Addr returns :8080.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ specs-pass\n✓ specs-untouched\n✓ suite-passes\n'implement' accepted.")

	// Every later stage runs again.
	v = r.expect("review", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.NotNil(t, v.Current.ReworkOf)
	r.workOnStage()
	r.submitAsAgent(reviewPass...)
	v = r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	r.workOnStage()
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ integration-passes")
	assert.Nil(t, r.status().Work, "work completed")

	types := r.eventTypes()
	assert.Equal(t, 1, count(types, "AssignmentHandedToFixer"))
	assert.Equal(t, 1, count(types, "FixerEntered"))
	assert.Equal(t, 1, count(types, "ReworkStarted"))
	assert.Equal(t, 0, count(types, "AttemptFailed"), "a code failure uses no attempt")
	assert.Equal(t, "WorkCompleted", types[len(types)-1])
}

// 2. Review finds a blocking problem; the fixer says the spec misses a case,
// so the work goes back to specify, whose new spec implement then meets.
func TestRework_ReviewFindsAProblem_TheFixerSendsItToSpecify(t *testing.T) {
	r := newReworkRepo(t, false)
	r.write(".gofast/checks.json", `{"integration": {"command": ["go", "test", "./..."]}, "timeout": "2m"}`)
	r.git("commit", "-q", "-am", "integration runs the unit tests")
	v := r.toReview()
	workID := v.Work.ID

	r.writeArtifact("Blocking: Addr still returns :8081.")
	out := r.submitAsAgent("--failed", "Addr still returns :8081")
	assert.Contains(t, out, "the work goes to the fixer")
	v = r.expect("fixer", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, "review", v.Current.Fixer.From)
	assert.Equal(t, "Addr still returns :8081", v.Current.Fixer.Reason)

	r.writeArtifact("Root cause: the spec only covers Port. Specify must add a spec for Addr.")
	r.submitAsAgent("--passed", "--route", "specify", "the spec misses Addr")

	// Specify's second visit, on its human gate.
	v = r.expect("specify", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, "human", v.Current.Gate)
	assert.Equal(t, "specify", v.Current.ReworkOf.Target)
	assert.Equal(t, ".gofast/works/"+workID+"/fixer-v1.md", v.Current.ReworkOf.FixerArtifact)
	r.write("server/addr_test.go", addrSpec)
	r.writeArtifact("TestAddr_Is8080.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ specs-fail")
	r.expect("specify", progress.AssignmentAwaitingApproval, 0, 3)
	r.gf("approve")

	// Implement's second visit must meet the new spec.
	v = r.expect("implement", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	r.writeArtifact("Nothing changed yet.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ specs-pass: 1 of 1 spec tests don't pass (TestAddr_Is8080)")
	r.expect("implement", progress.AssignmentOpen, 1, 3)
	r.write("server/addr.go", fixedAddr)
	r.writeArtifact("Addr returns :8080.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "'implement' accepted.")

	for _, stage := range []string{"review", "integration-testing"} {
		v = r.expect(stage, progress.AssignmentOpen, 0, 3)
		r.workOnStage()
		r.submitAsAgent(r.passed()...)
	}
	assert.Nil(t, r.status().Work, "work completed")
}

// 3. After three reworks, a fourth handover stops at the rework limit until
// the owner allows more from their own terminal.
func TestRework_TheFourthHandover_ReachesTheReworkLimit(t *testing.T) {
	r := newReworkRepo(t, false)
	r.toReview()
	for i := 1; i <= 3; i++ {
		r.writeArtifact("Blocking finding.")
		r.submitAsAgent("--failed", "blocking finding")
		v := r.expect("fixer", progress.AssignmentOpen, 0, 3)
		assert.Equal(t, i, v.Current.Visit)
		r.writeArtifact("Root cause.")
		r.submitAsAgent("--passed", "--route", "implement", "the code is wrong")
		r.expect("implement", progress.AssignmentOpen, 0, 3)
		r.writeArtifact("Changed.")
		r.submitAsAgent("--passed")
		v = r.expect("review", progress.AssignmentOpen, 0, 3)
		assert.Equal(t, i+1, v.Current.Visit)
		assert.Equal(t, i, v.Work.Reworks)
	}

	r.writeArtifact("Blocking finding.")
	r.submitAsAgent("--failed", "blocking finding")

	v := r.expect("review", progress.AssignmentHandedToFixer, 0, 3)
	assert.Equal(t, 4, v.Current.Visit)
	assert.Equal(t, 3, v.Work.Reworks)
	assert.Equal(t, 3, v.Work.ReworkBudget)
	assert.Equal(t, progress.NextOwner, v.Next.Actor)
	assert.Equal(t, []string{"gf rework-extend <n>", `gf return <stage> "<reason>"`, `gf abandon "<reason>"`}, v.Next.Commands)
	assert.Equal(t, 1, count(r.eventTypes(), "ReworkLimitReached"))

	// Nothing proceeds until the owner acts.
	res := r.runWithoutTerminal(asAgent, "submit", "--passed")
	assert.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "There is nothing to submit right now.")

	res = r.runWithoutTerminal(nil, "rework-extend", "1")
	assert.Equal(t, 1, res.code)
	assert.Equal(t, "Only the owner can allow more rework, from their own terminal. Run `gf rework-extend <n>` there.\n", res.stderr)
	res = r.gfFails(asAgent, "rework-extend", "1")
	assert.Equal(t, "Only the owner can allow more rework. Ask the owner to run `gf rework-extend <n>`.\n", res.stderr)
	r.expect("review", progress.AssignmentHandedToFixer, 0, 3)

	out := r.gf("rework-extend", "1")
	assert.Equal(t, "Extended the rework budget by 1 to 4. The work goes to the fixer; run /gofast:drive to continue.\n", out)
	v = r.expect("fixer", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 4, v.Current.Visit)
	assert.Equal(t, 4, v.Work.Reworks)
	assert.Equal(t, 4, v.Work.ReworkBudget)

	res = r.gfFails(nil, "rework-extend", "1")
	assert.Equal(t, "Rework can only be extended when the work reached its rework limit. Run `gf status` to see what happens next.\n", res.stderr)
}

// 4. A route the domain doesn't allow is a rejected command: no attempt is
// used and no check runs.
func TestRework_AnInvalidRoute_IsRejectedWithoutUsingAnAttempt(t *testing.T) {
	r := newReworkRepo(t, false)
	r.toReview()

	// Outside the fixer, a route is refused.
	res := r.runWithoutTerminal(asAgent, "submit", "--passed", "--route", "implement", "x")
	assert.Equal(t, 1, res.code)
	assert.Equal(t, "Only the fixer chooses a route. Submit with `gf submit --passed` or `gf submit --failed \"<reason>\"`. No attempt was used.\n", res.stderr)

	r.writeArtifact("Blocking finding.")
	r.submitAsAgent("--failed", "blocking finding")
	r.expect("fixer", progress.AssignmentOpen, 0, 3)
	r.writeArtifact("Root cause.")

	res = r.runWithoutTerminal(asAgent, "submit", "--passed", "--route", "review", "review it again")
	assert.Equal(t, 1, res.code)
	assert.Equal(t, "The fixer can only send the work back to an earlier stage that can fix it: specify, implement. 'review' is not one of them. No attempt was used.\n", res.stderr)
	assert.NotContains(t, res.stdout, "Running the exit checks")
	r.expect("fixer", progress.AssignmentOpen, 0, 3)

	res = r.runWithoutTerminal(asAgent, "submit", "--passed")
	assert.Equal(t, 1, res.code)
	assert.Equal(t, "The fixer says where the work goes: `gf submit --passed --route <specify|implement> \"<reason>\"`. No attempt was used.\n", res.stderr)

	res = r.runWithoutTerminal(asAgent, "submit", "--passed", "--route", "implement")
	assert.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "--route needs a reason")
	res = r.runWithoutTerminal(asAgent, "submit", "--failed", "x", "--route", "implement", "y")
	assert.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "--route goes with --passed")
	res = r.runWithoutTerminal(asAgent, "submit", "--passed", "--route", "nowhere", "y")
	assert.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "Unknown stage.")
	r.expect("fixer", progress.AssignmentOpen, 0, 3)

	r.submitAsAgent("--passed", "--route", "implement", "the code is wrong")
	r.expect("implement", progress.AssignmentOpen, 0, 3)
}
