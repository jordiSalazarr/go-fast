package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// Feature work on path v2, end to end: from the owner's event model or
// spec, through event modelling, grill, discovery, design and specify, to
// completion; and back to grill with gf return. Scenario 3, a v1 work
// started before path versions completing on its original path, is
// TestNewFeature_EndToEnd in design_e2e_test.go.

// walletModelJSON is the canonical event model of the brief's example.
func walletModelJSON(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../workflow/exitchecks/testdata/wallet-event-model.json")
	require.NoError(t, err)
	return string(data)
}

// walletModellingJSON is a design of the brief's example event model.
func walletModellingJSON(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../workflow/exitchecks/testdata/wallet-modelling.json")
	require.NoError(t, err)
	return string(data)
}

const (
	// ownersBoard is the owner's event model as they have it.
	ownersBoard   = "# Wallets board\n\nWalletOpened → PostTransaction → TransactionPosted | InsufficientFunds\nTransactionPosted → WalletBalance\n"
	modelMarkdown = "# Event model\n\nPostTransaction posts a transaction or rejects it with InsufficientFunds; WalletBalance shows the balance.\n\n" +
		"## Assumptions\n\n- M1: a wallet is opened before any transaction is posted.\n"
	// postingSpecs cover S1 and S3; overdraftSpecS2 covers S2.
	postingSpecs = `package server

import "testing"

// gofast:scenario S1
func TestPostTransaction_PostsADeposit(t *testing.T) {
	if got, err := PostTransaction(10, 5); err != nil || got != 15 {
		t.Fatalf("PostTransaction(10, 5) = %d, %v; want 15, nil", got, err)
	}
}

// gofast:scenario S3
func TestWalletBalance_ReflectsTheTransaction(t *testing.T) {
	if got, _ := PostTransaction(0, 7); got != 7 {
		t.Fatalf("balance = %d, want 7", got)
	}
}
`
	overdraftSpecS2 = `package server

import (
	"errors"
	"testing"
)

// gofast:scenario S2
func TestPostTransaction_RejectsAnOverdraft(t *testing.T) {
	if _, err := PostTransaction(10, -20); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
}
`
)

// toCompletion takes the work from implement through review and
// integration testing, skipping stress testing.
func (r *repo) toCompletion(posting string) {
	r.t.Helper()
	r.expect("implement", progress.AssignmentOpen, 0, 3)
	r.write("server/posting.go", posting)
	r.writeArtifact("Posting applies the amount and rejects overdrafts.")
	assert.Contains(r.t, r.submitAsAgent("--passed"), "'implement' accepted.")
	for _, stage := range []string{"review", "integration-testing"} {
		r.expect(stage, progress.AssignmentOpen, 0, 3)
		r.workOnStage()
		r.submitAsAgent(r.passed()...)
	}
	assert.Nil(r.t, r.status().Work, "work completed")
}

// 1. New feature from an event model: event modelling's first model misses
// a rejection scenario; grill settles its assumption with the owner; the
// index lists the grill; design's first placement maps a rejection to no
// invariant; specify first misses a scenario; the work completes.
func TestNewFeatureV2_FromAnEventModel_EndToEnd(t *testing.T) {
	r := newDesignRepo(t)
	r.write("docs/wallets-board.md", ownersBoard)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")

	r.gf("start", "--type", "new-feature", "Wallets", "--event-model", "docs/wallets-board.md")
	v := r.expect("event-modelling", progress.AssignmentOpen, 0, 3)
	id := v.Work.ID
	assert.Equal(t, "auto", v.Current.Gate)
	assert.Equal(t, []string{"artifact-written", "event-model-valid"}, v.Current.Checks)
	assert.True(t, v.Work.EventModel.Included)
	modelFile := ".gofast/works/" + id + "/event-model-v1.json"

	// Event modelling: a model without a scenario for InsufficientFunds fails.
	r.write(v.Current.Artifact, modelMarkdown)
	noRejection := strings.Replace(walletModelJSON(t), `"then": { "rejection": "InsufficientFunds" }`, `"then": { "events": ["TransactionPosted"] }`, 1)
	r.write(modelFile, noRejection)
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ event-model-valid: 1 problem: "+modelFile+": command slice PostTransaction has no scenario for rejection InsufficientFunds of command PostTransaction")
	v = r.expect("event-modelling", progress.AssignmentOpen, 1, 3)
	assert.Equal(t, []string{"command slice PostTransaction has no scenario for rejection InsufficientFunds of command PostTransaction"}, v.Current.EventModelProblems)
	r.write(modelFile, walletModelJSON(t))
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ event-model-valid\n'event-modelling' accepted.")

	// Grill, from the main session: every assumption is addressed; no
	// acceptance scenarios are needed with an event model.
	v = r.expect("grill", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 3, v.Work.EventModel.Scenarios)
	r.grillWrites(d1, "", decisionDocument(""))
	assert.Contains(t, r.submitAsAgent("--passed"), "✗ grill-valid: 1 problem: .gofast/works/"+id+"/grill-v1.json: assumption M1 of the event model is addressed by no decision")
	r.grillWrites(strings.Replace(d1, `"rationale"`, `"addresses": ["M1"], "rationale"`, 1), "", decisionDocument(""))
	r.submitAsAgent("--passed")
	r.expect("grill", progress.AssignmentAwaitingApproval, 1, 3)
	r.gf("approve")

	// The index lists the grill, with the event model, before discovery.
	index := r.readFile("docs/decisions/README.md")
	assert.Contains(t, index, "| Wallets | new-feature | "+id+" | main | in progress | [2026-10-12-"+id+"-wallets.md](2026-10-12-"+id+"-wallets.md) | [event-model-v1.json](../../.gofast/works/"+id+"/event-model-v1.json) |\n")

	// Discovery, on an auto gate.
	r.expect("discovery", progress.AssignmentOpen, 0, 3)
	r.writeArtifact("PostTransaction lands in server/posting.go; WalletBalance reads it.\n\n## Decisions for the owner\n\nNone.")
	assert.Contains(t, r.submitAsAgent("--passed"), "'discovery' accepted.")

	// Design: domain modelling; a rejection mapped to no invariant fails.
	r.expect("design", progress.AssignmentOpen, 0, 3)
	r.writeArtifact("The Wallet aggregate handles PostTransaction.")
	r.write(r.designFile(), strings.Replace(walletModellingJSON(t), `"InsufficientFunds": "balance never goes below zero"`, `"InsufficientFunds": "no overdrafts"`, 1))
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, `✗ design-valid: `+r.designFile()+` has 1 problem: rejection InsufficientFunds of command PostTransaction is mapped to "no overdrafts", which is not an invariant of aggregate Wallet`)
	r.write(r.designFile(), walletModellingJSON(t))
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ design-valid")
	v = r.expect("design", progress.AssignmentAwaitingApproval, 1, 3)
	r.gf("approve")

	// Specify covers the model's scenarios; S2 is missing first.
	v = r.expect("specify", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 1, v.Work.Design.Placements)
	assert.Equal(t, []string{"S1", "S2", "S3"}, v.Current.ScenariosToCover)
	r.write("server/posting_test.go", postingSpecs)
	r.writeArtifact("Specs for S1 and S3.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ specs-cover-scenarios: no spec written in this stage covers 1 scenario: S2")
	out = r.specifyPasses(map[string]string{"server/overdraft_test.go": overdraftSpecS2})
	assert.Contains(t, out, "✓ specs-fail\n✓ specs-cover-scenarios\n")
	r.gf("approve")

	r.toCompletion(postingV2)
	assert.Contains(t, r.readFile("docs/decisions/README.md"), "| "+id+" | main | completed |")
	types := r.eventTypes()
	assert.Equal(t, "WorkCompleted", types[len(types)-1])
}

// 2. New feature from a spec only: event modelling is skipped; grill needs
// acceptance scenarios; design uses the full schema; specify covers A1 and
// A2; the work completes.
func TestNewFeatureV2_FromASpec_EndToEnd(t *testing.T) {
	r := newDesignRepo(t)
	r.write("docs/wallets.md", walletsSpec)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")

	r.gf("start", "--type", "new-feature", "Wallets", "--spec", "docs/wallets.md")
	v := r.expect("grill", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, "skipped", stageStatuses(v)[0])
	assert.Equal(t, []progress.InputView{{Kind: "spec", Path: "docs/wallets.md", SHA256: v.Work.Inputs[0].SHA256}}, v.Work.Inputs)

	// Without an event model, grill needs acceptance scenarios.
	r.grillWrites(d1, "", decisionDocument(""))
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "without an event model, grill needs acceptance scenarios (A1, A2, …)")
	assert.Contains(t, out, "no '## Acceptance scenarios' section")
	a2 := `{"id": "A2", "given": "a wallet with 10", "when": "a credit of 5 is posted", "then": "its balance is 15"}`
	r.grillWrites(d1, a1+", "+a2, decisionDocument(a1Document+"\n- A2: Given a wallet with 10, when a credit of 5 is posted, then its balance is 15."))
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ grill-valid")
	r.gf("approve")

	r.expect("discovery", progress.AssignmentOpen, 0, 3)
	r.writeArtifact("Posting lives in server/posting.go.")
	r.submitAsAgent("--passed")

	// Design without an event model: the full schema.
	r.expect("design", progress.AssignmentOpen, 0, 3)
	r.designAwaitsApproval("")
	r.gf("approve")

	v = r.expect("specify", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"A1", "A2"}, v.Current.ScenariosToCover)
	r.write("server/posting_test.go", strings.Replace(postSpec, "func TestPostTransaction", "// gofast:scenario A2\nfunc TestPostTransaction", 1))
	r.writeArtifact("A2 only.")
	assert.Contains(t, r.submitAsAgent("--passed"), "✗ specs-cover-scenarios: no spec written in this stage covers 1 scenario: A1")
	out = r.specifyPasses(map[string]string{"server/overdraft_test.go": strings.Replace(overdraftSpec, "func Test", "// gofast:scenario A1\nfunc Test", 1)})
	assert.Contains(t, out, "✓ specs-cover-scenarios")
	r.gf("approve")

	r.toCompletion(postingV2)
}

// 4. The owner sends the work back from design to grill: grill's second
// visit runs, and the path goes on from there.
func TestReturn_FromDesignToGrill_StartsGrillsSecondVisit(t *testing.T) {
	r := newDesignRepo(t)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	r.gf("start", "--type", "new-feature", "Wallets")
	r.grillWrites(d1, a1, decisionDocument(a1Document))
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.writeArtifact("Posting lives in server/posting.go.")
	r.submitAsAgent("--passed")
	r.expect("design", progress.AssignmentOpen, 0, 3)

	r.gf("return", "grill", "refunds are in scope after all")
	v := r.expect("grill", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, &progress.ReturnedView{From: "design", FromVisit: 1, Target: "grill", TargetVisit: 2, Reason: "refunds are in scope after all"}, v.Current.Returned)
	assert.Equal(t, 1, v.Work.Grill.Visit, "the latest grill outcome is still v1's until v2 writes one")

	// Grill's second visit updates the same decision document.
	d2 := `{"id": "D2", "question": "Are refunds in scope?", "answer": "Yes.", "rationale": "The owner changed their mind."}`
	r.grillWrites(d1+", "+d2, a1, decisionDocument(a1Document))
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ grill-valid")
	v = r.expect("grill", progress.AssignmentAwaitingApproval, 0, 3)
	assert.Equal(t, 2, v.Work.Grill.Visit)
	assert.Equal(t, 2, v.Work.Grill.Decisions)
	r.gf("approve")

	v = r.expect("discovery", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, 1, strings.Count(r.readFile("docs/decisions/README.md"), "| Wallets |"), "one row per work")
	types := r.eventTypes()
	assert.Equal(t, 1, count(types, "WorkReturned"))
	assert.Equal(t, 1, count(types, "AssignmentCancelled"), "design's first visit")
}
