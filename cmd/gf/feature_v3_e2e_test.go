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

// The wallet code of the v3 end-to-end test, which starts with no wallet
// code at all.
const (
	// walletSpecs pin A1 and A2; they need PostTransaction and
	// ErrInsufficientFunds, which don't exist yet.
	walletSpecs = `package server

import (
	"errors"
	"testing"
)

// gofast:scenario A1
func TestPostTransaction_RejectsAnOverdraft(t *testing.T) {
	if _, err := PostTransaction(10, -20); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
}

// gofast:scenario A2
func TestPostTransaction_PostsACredit(t *testing.T) {
	if got, err := PostTransaction(10, 5); err != nil || got != 15 {
		t.Fatalf("PostTransaction(10, 5) = %d, %v; want 15, nil", got, err)
	}
}
`
	// walletSkeleton declares what the specs need and does nothing.
	walletSkeleton = `package server

import "errors"

var errSkeleton = errors.New("skeleton: not implemented")

type postingError string

func (e postingError) Error() string { return "" }

// ErrInsufficientFunds rejects a debit larger than the balance.
const ErrInsufficientFunds = postingError("insufficient funds")

// PostTransaction applies a transaction to a wallet's balance.
func PostTransaction(balance, amount int) (int, error) { return 0, errSkeleton }
`
	// walletCode fills the skeleton.
	walletCode = `package server

type postingError string

func (e postingError) Error() string { return string(e) }

// ErrInsufficientFunds rejects a debit larger than the balance.
const ErrInsufficientFunds = postingError("insufficient funds")

// PostTransaction applies a transaction to a wallet's balance.
func PostTransaction(balance, amount int) (int, error) {
	if balance+amount < 0 {
		return balance, ErrInsufficientFunds
	}
	return balance + amount, nil
}
`
	glossary = "# Glossary\n\n**Wallet**: a customer's balance.\n"
	adr      = "# Wallets never lend\n\nA debit never takes a balance below zero: lending is a product we don't offer.\n"
)

// New feature work on v3, from a spec only, end to end: discovery's
// questions, grill writing the glossary and an ADR, the index before
// design's baseline, specify's skeleton, implement filling it, and review's
// verdict.
func TestNewFeatureV3_FromASpec_EndToEnd(t *testing.T) {
	r := newRepo(t)
	r.configureChecks()
	r.write("docs/wallets.md", walletsSpec)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")

	r.gf("start", "--type", "new-feature", "Wallets", "--spec", "docs/wallets.md")
	v := r.expect("discovery", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, "v3", v.Work.PathVersion)
	r.writeArtifact(discoveryBeforeGrill)
	assert.Contains(t, r.submitAsAgent("--passed"), "'discovery' accepted.")

	// Grill, from the main session: the decision document, the glossary and
	// an ADR.
	r.expect("grill", progress.AssignmentOpen, 0, 3)
	a2 := `{"id": "A2", "given": "a wallet with 10", "when": "a credit of 5 is posted", "then": "its balance is 15"}`
	r.write("GLOSSARY.md", glossary)
	r.write("docs/adr/0001-wallets-never-lend.md", adr)
	r.grillWrites(d1, a1+", "+a2, decisionDocument(a1Document+"\n- A2: Given a wallet with 10, when a credit of 5 is posted, then its balance is 15."))
	out := r.submitAsAgent("--passed")
	assert.NotContains(t, out, "write scope")
	assert.Contains(t, out, "✓ grill-valid")
	r.gf("approve")
	assert.Contains(t, r.readFile("docs/decisions/README.md"), "| Wallets | new-feature | "+v.Work.ID+" | main | in progress |")

	// Design: the index was written before its baseline, so its scope check
	// doesn't see it.
	r.expect("design", progress.AssignmentOpen, 0, 3)
	r.designAwaitsApproval("")
	r.gf("approve")

	// Specify: a skeleton with logic fails skeletons-only first; a skeleton
	// that only declares lets the specs compile and fail.
	v = r.expect("specify", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"A1", "A2"}, v.Current.ScenariosToCover)
	r.write("server/posting_test.go", walletSpecs)
	r.write("server/posting.go", strings.Replace(walletSkeleton, "{ return 0, errSkeleton }", "{\n\tif amount < 0 {\n\t\treturn 0, errSkeleton\n\t}\n\treturn 0, errSkeleton\n}", 1))
	r.writeArtifact("## Specs\n\nA1, A2.\n\n## Skeletons\n\nserver/posting.go: PostTransaction, ErrInsufficientFunds.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ skeletons-only: server/posting.go: func PostTransaction has logic")
	r.expect("specify", progress.AssignmentOpen, 1, 3)
	r.write("server/posting.go", walletSkeleton)
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ skeletons-only\n✓ specs-fail\n✓ specs-cover-scenarios")
	r.expect("specify", progress.AssignmentAwaitingApproval, 1, 3)
	r.gf("approve")

	// Implement: leaving errSkeleton fails skeletons-filled; filling the
	// skeleton passes.
	r.expect("implement", progress.AssignmentOpen, 0, 3)
	r.write("server/posting.go", walletCode+"\nvar errSkeleton = postingError(\"skeleton\")\n")
	r.writeArtifact("Posting applies the amount and rejects overdrafts.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ skeletons-filled: errSkeleton is still in server/posting.go; fill every skeleton specify wrote")
	r.expect("implement", progress.AssignmentOpen, 1, 3)
	r.write("server/posting.go", walletCode)
	assert.Contains(t, r.submitAsAgent("--passed"), "'implement' accepted.")

	// Review: a pass whose artifact lists a blocking finding retries review.
	v = r.expect("review", progress.AssignmentOpen, 0, 3)
	r.write(v.Current.Artifact, reviewArtifact("Posting rejects overdrafts.")+"## Blocking\n### B1 a debit can overdraw\n")
	out = r.submitAsAgent(r.passed()...)
	assert.Contains(t, out, "✗ review-verdict-consistent: "+v.Current.Artifact+": the verdict says Passed but ## Blocking lists B1")
	v = r.expect("review", progress.AssignmentOpen, 1, 3)
	assert.Equal(t, 0, v.Work.Reworks)
	r.writeArtifact("Posting rejects overdrafts.")
	assert.Contains(t, r.submitAsAgent(r.passed()...), "✓ review-verdict-consistent")

	r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	r.workOnStage()
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ integration-passes")
	assert.Nil(t, r.status().Work, "work completed")
	assert.Equal(t, glossary, r.readFile("GLOSSARY.md"))
}
