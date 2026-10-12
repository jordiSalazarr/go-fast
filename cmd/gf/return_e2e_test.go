package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// The owner sends work on implement back to specify: implement's visit is
// cancelled, specify's second visit reads the reason, and the path runs on
// from there.
func TestReturn_FromImplementToSpecify_EndToEnd(t *testing.T) {
	r := newRepo(t)
	r.configureChecks()
	r.gf("start", "--type", "fix-bug", "the server listens on the wrong port")
	for _, stage := range []string{"discovery", "specify"} {
		r.expect(stage, progress.AssignmentOpen, 0, 3)
		r.workOnStage()
		r.submitAsAgent("--passed")
		r.gf("approve")
	}
	r.expect("implement", progress.AssignmentOpen, 0, 3)

	res := r.gfFails(asAgent, "return", "specify", "the specs miss a case")
	assert.Equal(t, "Only the owner can send work back. Ask the owner to run `gf return <stage> \"<reason>\"`.\n", res.stderr)
	res = r.gfFails(nil, "return", "review", "too early")
	assert.Equal(t, "The work can only go back to an earlier stage of its path that it entered: discovery, specify. 'review' is not one of them.\n", res.stderr)

	out := r.gf("return", "specify", "the specs miss a case")
	assert.Equal(t, "Returned fix-bug work \"the server listens on the wrong port\" from 'implement' to 'specify'. Run /gofast:drive to continue.\n", out)
	v := r.expect("specify", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	require.NotNil(t, v.Current.Returned)
	assert.Equal(t, progress.ReturnedView{From: "implement", FromVisit: 1, Target: "specify", TargetVisit: 2, Reason: "the specs miss a case"}, *v.Current.Returned)
	assert.Contains(t, r.gf("status"), "Returned: the owner sent the work back from implement (visit 1) to specify (visit 2): the specs miss a case\n")
	assert.Equal(t, 0, v.Work.Reworks, "a return is not a rework")

	// Specify's second visit writes a new spec; the path runs on.
	r.write("server/port_again_test.go", "package server\n\nimport \"testing\"\n\nfunc TestPort_IsStill8080(t *testing.T) {\n\tif Port() != 8080 {\n\t\tt.Fatal(\"want 8080\")\n\t}\n}\n")
	r.writeArtifact("Another spec.")
	r.submitAsAgent("--passed")
	r.gf("approve")
	for _, stage := range []string{"implement", "review", "integration-testing"} {
		r.expect(stage, progress.AssignmentOpen, 0, 3)
		r.workOnStage()
		r.submitAsAgent(r.passed()...)
	}
	assert.Nil(t, r.status().Work, "work completed")

	types := r.eventTypes()
	assert.Equal(t, 1, count(types, "WorkReturned"))
	assert.Equal(t, 1, count(types, "AssignmentCancelled"), "implement's first visit")
	assert.Equal(t, "WorkCompleted", types[len(types)-1])
}
