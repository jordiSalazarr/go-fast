package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// Feature work on path v3: discovery maps the owner's input onto the code
// first, then grill settles discovery's questions with the owner.

// discoveryBeforeGrill is discovery's artifact on v3 feature work: it
// describes what exists and leaves the owner's questions to grill.
const discoveryBeforeGrill = "## Question\n\nWhere do wallets fit the code?\n\n## Answer\n\nPosting lives in server/posting.go; there is no wallet yet.\n\n" +
	"## Evidence\n\n| # | Fact | Evidence |\n|---|---|---|\n| E1 | postings have no balance | server/posting.go:1 |\n\n" +
	"## Questions for grill\n\n- Can a debit take the balance below zero? Nothing in the code decides it (E1).\n"

// New feature works start on v3: discovery (auto gate), then grill. The
// owner's approval of grill regenerates the decisions index before design's
// visit is entered, so design's baseline has it and its scope check
// doesn't see it change.
func TestNewFeatureV3_DiscoversBeforeGrill_AndTheIndexComesBeforeDesign(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "new-feature", "Wallets")
	v := r.expect("discovery", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, "v3", v.Work.PathVersion)
	assert.Equal(t, "auto", v.Current.Gate)
	assert.Nil(t, v.Work.Grill)
	assert.Equal(t, []string{"artifact-written"}, v.Current.Checks)
	assert.Contains(t, v.Next.Message, "end the artifact with `## Questions for grill`; grill settles them with the owner next.")
	assert.Equal(t, []string{"skipped", "current", "pending", "pending", "pending", "pending", "pending", "pending", "pending"}, stageStatuses(v))

	r.writeArtifact(discoveryBeforeGrill)
	assert.Contains(t, r.submitAsAgent("--passed"), "'discovery' accepted.")
	v = r.expect("grill", progress.AssignmentOpen, 0, 3)
	assert.Contains(t, v.Next.Message, "load the gofast:grill skill, start from discovery's `## Questions for grill`")

	r.grillWrites(d1, a1, decisionDocument(a1Document))
	r.submitAsAgent("--passed")
	_, err := os.Stat(filepath.Join(r.dir, "docs", "decisions", "README.md"))
	assert.ErrorIs(t, err, os.ErrNotExist, "not before the owner approves")
	r.gf("approve")
	id := r.status().Work.ID
	assert.Contains(t, r.readFile("docs/decisions/README.md"), "| Wallets | new-feature | "+id+" | main | in progress |")

	r.expect("design", progress.AssignmentOpen, 0, 3)
	r.writeArtifact("Wallets.")
	out := r.submitAsAgent("--failed", "not done")
	assert.NotContains(t, out, "write scope", "the index was written before design's baseline")
}

// On v3 the owner may send feature work back to grill or to discovery, as
// to any stage it entered earlier; the fixer never routes to grill.
func TestNewFeatureV3_TheOwnerReturnsTheWorkToGrillAndToDiscovery(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "new-feature", "Wallets")
	r.writeArtifact(discoveryBeforeGrill)
	r.submitAsAgent("--passed")
	r.grillWrites(d1, a1, decisionDocument(a1Document))
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.expect("design", progress.AssignmentOpen, 0, 3)

	r.gf("return", "grill", "refunds are in scope after all")
	v := r.expect("grill", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	r.grillWrites(d1, a1, decisionDocument(a1Document))
	require.Contains(t, r.submitAsAgent("--passed"), "✓ grill-valid")
	r.gf("approve")
	r.expect("design", progress.AssignmentOpen, 0, 3)

	r.gf("return", "discovery", "the posting code moved")
	v = r.expect("discovery", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, &progress.ReturnedView{From: "design", FromVisit: 2, Target: "discovery", TargetVisit: 2, Reason: "the posting code moved"}, v.Current.Returned)
	r.writeArtifact(discoveryBeforeGrill)
	r.submitAsAgent("--passed")
	assert.Equal(t, 3, r.expect("grill", progress.AssignmentOpen, 0, 3).Current.Visit, "grill runs again after discovery")
}
