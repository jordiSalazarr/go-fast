package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// decisionDocument is a complete decision document; acceptance lists its
// acceptance scenarios, empty when the work has an event model.
func decisionDocument(acceptance string) string {
	doc := "# Wallets\n\n## Intent\n\nCustomers hold a balance and post transactions.\n\n" +
		"## Scope\n\nIn: debits and credits. Out: refunds.\n\n" +
		"## Decisions\n\n- D1: Can a debit take the balance below zero? No: wallets never lend.\n\n" +
		"## Glossary\n\nWallet: a customer's balance.\n\n"
	if acceptance != "" {
		doc += "## Acceptance scenarios\n\n" + acceptance + "\n"
	}
	return doc + "## Open questions\n\nNone.\n"
}

// grillJSON is grill's structured outcome: decisions as JSON objects,
// acceptance scenarios likewise.
func grillJSON(docPath, decisions, scenarios string) string {
	return fmt.Sprintf(`{"decisions": [%s], "acceptanceScenarios": [%s], "openQuestions": [], "docPath": %q}`, decisions, scenarios, docPath)
}

const (
	d1         = `{"id": "D1", "question": "Can a debit take the balance below zero?", "answer": "No.", "rationale": "Wallets never lend."}`
	a1         = `{"id": "A1", "given": "a wallet with 10", "when": "a debit of 20 is posted", "then": "it is rejected for insufficient funds"}`
	a1Document = "- A1: Given a wallet with 10, when a debit of 20 is posted, then it is rejected for insufficient funds."
)

// docPathOf is the work's decision document in the default directory.
func (r *repo) docPathOf() string {
	r.t.Helper()
	w := r.status().Work
	require.NotNil(r.t, w)
	return w.DecisionsDir + "/2026-10-12-" + w.ID + "-wallets.md"
}

// grillWrites writes grill's outputs, as the main session does.
func (r *repo) grillWrites(decisions, scenarios, document string) {
	r.t.Helper()
	doc := r.docPathOf()
	r.write(doc, document)
	r.write(strings.TrimSuffix(r.status().Current.Artifact, ".md")+".json", grillJSON(doc, decisions, scenarios))
	r.writeArtifact("The plan is in " + doc + ".")
}

// Grill runs in the main session: it writes the decision document and its
// outputs, submits, and the owner approves the plan.
func TestGrill_TheMainSessionSubmits_AndTheOwnerApprovesThePlan(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "new-feature", "Wallets")
	v := r.expect("grill", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "grill-valid"}, v.Current.Checks)
	assert.Equal(t, "docs/decisions", v.Work.DecisionsDir)
	assert.Contains(t, v.Next.Message, "Grill with the owner in this session, not in a subagent: load the gofast:grill skill")

	// Only this work's decision document is in grill's scope.
	r.grillWrites(d1, a1, decisionDocument(a1Document))
	r.write("docs/decisions/notes.md", "scratch")
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "Changed files outside the 'grill' write scope: docs/decisions/notes.md.")
	r.remove("docs/decisions/notes.md")

	// An open question fails grill-valid.
	r.write(strings.TrimSuffix(v.Current.Artifact, ".md")+".json",
		strings.Replace(grillJSON(r.docPathOf(), d1, a1), `"openQuestions": []`, `"openQuestions": ["Who may freeze a wallet?"]`, 1))
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ grill-valid: 1 problem: "+strings.TrimSuffix(v.Current.Artifact, ".md")+".json: 1 question is still open: Who may freeze a wallet?")
	r.expect("grill", progress.AssignmentOpen, 2, 3)

	r.grillWrites(d1, a1, decisionDocument(a1Document))
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ grill-valid\n'grill' passed its exit checks and waits for the owner")
	v = r.expect("grill", progress.AssignmentAwaitingApproval, 2, 3)
	require.NotNil(t, v.Work.Grill)
	assert.Equal(t, progress.GrillView{Visit: 1, File: strings.TrimSuffix(v.Current.Artifact, ".md") + ".json", DocPath: r.docPathOf(), Decisions: 1, AcceptanceScenarios: 1}, *v.Work.Grill)
	assert.Equal(t, "Waiting for the owner to approve 'grill': read the decision document "+r.docPathOf()+". Approving it approves the plan: run `gf approve`, or `gf reject \"<feedback>\"` for another round.", v.Next.Message)

	r.gf("approve")
	v = r.expect("discovery", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, "auto", v.Current.Gate)
	assert.True(t, v.Work.Grill.Accepted)
}

func (r *repo) remove(name string) {
	r.t.Helper()
	require.NoError(r.t, os.Remove(filepath.Join(r.dir, filepath.FromSlash(name))))
}

// The owner's approval of grill regenerates the decisions index before
// discovery's visit is entered, so discovery's baseline has it and its scope
// check doesn't see it change. Abandoning the work updates its status.
func TestDecisionsIndex_IsWrittenBeforeDiscoveryBegins_AndFollowsTheWorksStatus(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "new-feature", "Wallets")
	r.grillWrites(d1, a1, decisionDocument(a1Document))
	r.submitAsAgent("--passed")
	_, err := os.Stat(filepath.Join(r.dir, "docs", "decisions", "README.md"))
	assert.ErrorIs(t, err, os.ErrNotExist, "not before the owner approves")

	r.gf("approve")
	id := r.status().Work.ID
	index := r.readFile("docs/decisions/README.md")
	assert.Contains(t, index, "<!-- Generated by gf from the accepted grills.")
	assert.Contains(t, index, "| Wallets | new-feature | "+id+" | main | in progress | [2026-10-12-"+id+"-wallets.md](2026-10-12-"+id+"-wallets.md) | — |\n")

	// Discovery's scope check doesn't see the index: it was written before
	// discovery's baseline.
	r.expect("discovery", progress.AssignmentOpen, 0, 3)
	r.writeArtifact("The wallet code lives in server/.")
	out := r.submitAsAgent("--passed")
	assert.NotContains(t, out, "write scope")
	assert.Contains(t, out, "'discovery' accepted.")

	// A stage that writes the index fails its scope check.
	r.expect("design", progress.AssignmentOpen, 0, 3)
	r.write("docs/decisions/README.md", "mine")
	r.writeArtifact("Wallets.")
	out = r.submitAsAgent("--failed", "not done")
	assert.Contains(t, out, "Changed files outside the 'design' write scope: docs/decisions/README.md.")
	r.write("docs/decisions/README.md", index)

	r.gf("abandon", "wrong idea")
	assert.Contains(t, r.readFile("docs/decisions/README.md"), "| "+id+" | main | abandoned |")
}

func TestDecisionsIndex_GoesInTheConfiguredDirectory(t *testing.T) {
	r := newRepo(t)
	r.write(".gofast/checks.json", `{"docs": {"decisionsDir": "adr"}}`)
	r.gf("start", "--type", "new-feature", "Wallets")
	assert.Equal(t, "adr", r.status().Work.DecisionsDir)
	assert.Contains(t, r.gf("checks"), "  decisions    adr/ (grill's decision documents; gf generates its README.md)\n")

	r.grillWrites(d1, a1, decisionDocument(a1Document))
	r.submitAsAgent("--passed")
	r.gf("approve")
	assert.Contains(t, r.readFile("adr/README.md"), "| Wallets | new-feature |")
}
