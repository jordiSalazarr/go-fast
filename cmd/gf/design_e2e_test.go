package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// The design stage end to end, on path v1: new-feature and modify-behaviour
// work started before paths had versions, NFR proposals the owner adopts,
// and rework back into design. Such works keep their path: discovery
// (human gate), design, specify.

// walletDesignJSON is the brief's example design.
func walletDesignJSON(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "workflow", "exitchecks", "testdata", "wallet-design.json"))
	require.NoError(t, err)
	return string(data)
}

// behaviourDelta is modify-behaviour's discovery artifact.
const behaviourDelta = "# discovery\n\n## Current behaviour\n\nThe server listens on 8081.\n\n## Expected behaviour\n\nThe server listens on 8080.\n"

// latencyProposal proposes an NFR on the server package.
const latencyProposal = `{"nfrs": [{"id": "server-latency", "areas": ["server/**"], "operation": "GET /port",
  "load": {"vus": 2, "duration": "1s"}, "targets": {"p95": "500ms", "minRps": 1}}]}`

// startV1 starts work the way gf did before paths had versions: its
// WorkStarted has no path version, so it follows path v1. Like gf start, it
// runs the automations, which open the first assignment and record its
// baseline.
func (r *repo) startV1(workType, description string) {
	r.t.Helper()
	r.startOn(domain.PathVersion{}, "w-v1", workType, description)
	v := r.expect("discovery", progress.AssignmentOpen, 0, 3)
	require.Equal(r.t, "human", v.Current.Gate, "path v1")
}

// startV2 starts feature work the way gf did while new feature works started
// on path v2, from the inputs gf start's flags name (--spec, --event-model):
// grill first, or event modelling with an event model.
func (r *repo) startV2(workType, description string, flags ...string) {
	r.t.Helper()
	var inputs []domain.WorkInput
	for i := 0; i+1 < len(flags); i += 2 {
		kind := map[string]domain.InputKind{"--spec": domain.InputSpec, "--event-model": domain.InputEventModel}[flags[i]]
		content, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(flags[i+1])))
		require.NoError(r.t, err)
		inputs = append(inputs, must(domain.NewWorkInput(kind, must(domain.ParseRepoPath(flags[i+1])), domain.HashOf(content))))
	}
	r.startOn(domain.PathV2, "w-v2", workType, description, inputs...)
	require.Equal(r.t, "v2", r.status().Work.PathVersion)
}

// startOn records the start of a work on a path version, as gf start did
// when new works started on it, then runs the automations like gf start.
func (r *repo) startOn(version domain.PathVersion, workID, workType, description string, inputs ...domain.WorkInput) {
	r.t.Helper()
	store, err := eventlog.Open(r.dir)
	require.NoError(r.t, err)
	id := must(domain.NewWorkID(workID))
	started := domain.WorkStarted{WorkID: id, WorkType: must(domain.ParseWorkType(workType)), PathVersion: version,
		Description: must(domain.NewDescription(description)), Branch: must(domain.NewBranch("main")), Inputs: inputs}
	first := firstStepOf(must(domain.PathOf(started.WorkType, started.PathVersion)), inputs)
	opened := func(a domain.AssignmentID) {
		_, err := worktree.OpenBaselines(r.dir).Ensure(a)
		require.NoError(r.t, err)
	}
	require.NoError(r.t, store.Exclusive(func(s *eventlog.Session) error {
		err := s.Append(eventlog.WorkStream(id), 0, eventlog.OwnerActor(must(domain.NewOwner("Jordi", "jordi@example.com"))),
			started, domain.StageEntered{WorkID: id, Stage: first.Stage(), Visit: domain.FirstVisit(), Gate: first.Gate(), Budget: first.Budget()})
		if err != nil {
			return err
		}
		return automations.Runner{AssignmentOpened: opened}.Run(s)
	}))
}

// firstStepOf is the first step of a path a work with these inputs enters:
// event modelling only with an event model, and no other optional step.
func firstStepOf(path domain.Path, inputs []domain.WorkInput) domain.PathStep {
	for _, step := range path.Steps() {
		if !step.Optional() {
			return step
		}
		if step.Stage() == domain.StageEventModelling && slices.ContainsFunc(inputs, func(i domain.WorkInput) bool { return i.Kind() == domain.InputEventModel }) {
			return step
		}
	}
	panic("a path without a step to enter")
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// toDesign commits the repository, starts v1 work of a designed type and
// takes it through discovery, to design's first visit.
func (r *repo) toDesign(workType string) progress.View {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	r.startV1(workType, "The server listens on 8080")
	r.write(r.status().Current.Artifact, behaviourDelta)
	r.submitAsAgent("--passed")
	r.gf("approve")
	return r.expect("design", progress.AssignmentOpen, 0, 3)
}

// designFile is the structured design of the current design visit.
func (r *repo) designFile() string {
	r.t.Helper()
	return strings.TrimSuffix(r.status().Current.Artifact, ".md") + ".json"
}

// proposalsFile is where the current design visit proposes NFRs.
func (r *repo) proposalsFile() string {
	r.t.Helper()
	c := r.status().Current
	return filepath.ToSlash(filepath.Join(filepath.Dir(c.Artifact), "nfrs.proposed-v"+strings.TrimPrefix(strings.TrimSuffix(filepath.Base(c.Artifact), ".md"), "design-v")+".json"))
}

// designAwaitsApproval writes a valid design, with NFR proposals if any,
// and submits it.
func (r *repo) designAwaitsApproval(proposals string) {
	r.t.Helper()
	r.writeArtifact("Wallets: event model, aggregates, slices, contracts.")
	r.write(r.designFile(), walletDesignJSON(r.t))
	if proposals != "" {
		r.write(r.proposalsFile(), proposals)
	}
	out := r.submitAsAgent("--passed")
	assert.Contains(r.t, out, "✓ design-valid\n✓ nfr-proposals-valid\n")
	c := r.status().Current
	assert.Equal(r.t, "design", c.Stage)
	assert.Equal(r.t, progress.AssignmentAwaitingApproval, c.Assignment)
}

// runWithInput runs gf as the owner in a terminal, typing input.
func (r *repo) runWithInput(input string, args ...string) result {
	r.t.Helper()
	var stdout, stderr bytes.Buffer
	code := execute(append([]string{"--dir", r.dir}, args...), func(string) string { return "" },
		func() bool { return true }, strings.NewReader(input), &stdout, &stderr)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (r *repo) readFile(name string) string {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, filepath.FromSlash(name)))
	require.NoError(r.t, err)
	return string(data)
}

func TestNFRsAdopt_IsAllowedOnlyWhileDesignAwaitsApproval(t *testing.T) {
	r := newRepo(t)
	r.toDesign("new-feature")
	r.write(r.proposalsFile(), latencyProposal)

	res := r.gfFails(nil, "nfrs", "adopt", "--yes")
	assert.Equal(t, "NFR proposals can only be adopted while design waits for your approval: after design submits, before `gf approve`. Run `gf status` to see where the work is.\n", res.stderr)

	r.designAwaitsApproval(latencyProposal)
	r.gf("nfrs", "adopt", "--yes")
	r.gf("approve")

	res = r.gfFails(nil, "nfrs", "adopt", "--yes")
	assert.Contains(t, res.stderr, "NFR proposals can only be adopted while design waits for your approval")
}

func TestNFRsAdopt_IsOwnerOnly_FromATerminal(t *testing.T) {
	r := newRepo(t)
	r.toDesign("new-feature")
	r.designAwaitsApproval(latencyProposal)

	res := r.runWithoutTerminal(nil, "nfrs", "adopt", "--yes")
	assert.Equal(t, 1, res.code)
	assert.Equal(t, "Only the owner can adopt NFR proposals, from their own terminal. Run `gf nfrs adopt` there.\n", res.stderr)

	res = r.gfFails(asAgent, "nfrs", "adopt", "--yes")
	assert.Equal(t, "Only the owner can adopt NFR proposals. Ask the owner to run `gf nfrs adopt`.\n", res.stderr)

	_, err := os.Stat(filepath.Join(r.dir, ".gofast", "nfrs.json"))
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing adopted")
}

func TestNFRsAdopt_ShowsTheChanges_AsksFirst_AndMergesThem(t *testing.T) {
	r := newRepo(t)
	r.write(".gofast/nfrs.json", `{"nfrs": [
  {"id": "server-latency", "areas": ["server/**"], "operation": "GET /port", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "900ms"}},
  {"id": "health", "description": "kept as the owner wrote it", "areas": ["health/**"], "operation": "GET /healthz",
   "load": {"vus": 1, "duration": "1s"}, "targets": {"maxErrorRate": 0}}
]}`)
	r.toDesign("new-feature")
	proposals := `{"nfrs": [
  {"id": "server-latency", "areas": ["server/**"], "operation": "GET /port", "load": {"vus": 2, "duration": "1s"}, "targets": {"p95": "500ms", "minRps": 1}},
  {"id": "health", "description": "kept as the owner wrote it", "areas": ["health/**"], "operation": "GET /healthz",
   "load": {"vus": 1, "duration": "1s"}, "targets": {"maxErrorRate": 0}},
  {"id": "port-throughput", "areas": ["server/**"], "operation": "GET /port", "load": {"vus": 4, "duration": "2s"}, "targets": {"minRps": 50}}
]}`
	r.designAwaitsApproval(proposals)
	file := r.proposalsFile()

	v := r.status()
	require.NotNil(t, v.Work.NFRProposals)
	assert.Equal(t, &progress.NFRProposalsView{File: file, Added: []string{"port-throughput"}, Replaced: []string{"server-latency"}}, v.Work.NFRProposals)
	assert.Contains(t, r.gf("status"), "NFR proposals pending adoption: "+file+" (adds port-throughput; replaces server-latency).")

	// Declined: nothing changes.
	res := r.runWithInput("n\n", "nfrs", "adopt")
	require.Equal(t, 0, res.code, res.stderr)
	assert.Equal(t, "Design (visit 1) proposes these NFR changes ("+file+"):\n"+
		"  + add port-throughput: GET /port (server/**), 4 vus for 2s; rps ≥ 50\n"+
		"  ~ replace server-latency\n"+
		"      was: GET /port (server/**), 1 vus for 1s; p95 ≤ 900ms\n"+
		"      now: GET /port (server/**), 2 vus for 1s; p95 ≤ 500ms, rps ≥ 1\n"+
		"Adopt them into .gofast/nfrs.json? [y/N] Nothing adopted.\n", res.stdout)
	assert.Contains(t, r.readFile(".gofast/nfrs.json"), `"p95": "900ms"`)

	// Confirmed: replaced in place, added at the end, the rest kept.
	res = r.runWithInput("y\n", "nfrs", "adopt")
	require.Equal(t, 0, res.code, res.stderr)
	assert.Contains(t, res.stdout, "Adopt them into .gofast/nfrs.json? [y/N] Adopted 2 NFR changes into .gofast/nfrs.json. Commit it now (`git add .gofast/nfrs.json && git commit`), then run `gf approve`.\n")
	nfrs := r.readFile(".gofast/nfrs.json")
	latency, health, throughput := strings.Index(nfrs, `"server-latency"`), strings.Index(nfrs, `"health"`), strings.Index(nfrs, `"port-throughput"`)
	assert.True(t, latency >= 0 && latency < health && health < throughput, nfrs)
	assert.Contains(t, nfrs, `"p95": "500ms"`)
	assert.NotContains(t, nfrs, `"900ms"`)
	assert.Contains(t, r.gf("checks"), "port-throughput")
	assert.Nil(t, r.status().Work.NFRProposals, "nothing pending")

	// Adopting again changes nothing; nothing is recorded in the event log.
	assert.Equal(t, ".gofast/nfrs.json already has the NFRs design proposes in "+file+": nothing to adopt.\n", r.gf("nfrs", "adopt"))
	assert.NotContains(t, strings.Join(r.eventTypes(), " "), "NFR")
}

func TestNFRsAdopt_CreatesTheNFRsFile(t *testing.T) {
	r := newRepo(t)
	r.toDesign("new-feature")
	r.designAwaitsApproval(latencyProposal)

	out := r.gf("nfrs", "adopt", "--yes")

	assert.Contains(t, out, "  + add server-latency: GET /port (server/**), 2 vus for 1s; p95 ≤ 500ms, rps ≥ 1\n")
	assert.Contains(t, out, "Adopted 1 NFR change into .gofast/nfrs.json.")
	assert.Contains(t, r.gf("checks"), "server-latency")
}

func TestNFRsAdopt_RefusesInvalidProposals_AndMissingOnes(t *testing.T) {
	r := newRepo(t)
	r.toDesign("new-feature")
	r.designAwaitsApproval("")

	res := r.gfFails(nil, "nfrs", "adopt", "--yes")
	assert.Equal(t, "This design visit proposes no NFRs: its nfrs.proposed-v<visit>.json does not exist. There is nothing to adopt.\n", res.stderr)

	// Changed by hand after design's checks passed.
	r.write(r.proposalsFile(), `{"nfrs": [{"id": "x", "areas": ["server/**"], "operation": "GET /port"}]}`)
	res = r.gfFails(nil, "nfrs", "adopt", "--yes")
	assert.Equal(t, "The NFR proposals "+r.proposalsFile()+" are invalid: nfrs[0] (\"x\"): \"load\" is required, e.g. {\"vus\": 50, \"duration\": \"60s\"}. Reject the design with `gf reject \"<feedback>\"` so it fixes them.\n", res.stderr)
	_, err := os.Stat(filepath.Join(r.dir, ".gofast", "nfrs.json"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestChecks_WarnsAboutNFRsWithoutMinRPS(t *testing.T) {
	r := newRepo(t)
	r.write(".gofast/nfrs.json", `{"nfrs": [
  {"id": "latency-only", "areas": ["server/**"], "operation": "GET /port", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "900ms"}},
  {"id": "with-rps", "areas": ["server/**"], "operation": "GET /port", "load": {"vus": 1, "duration": "1s"}, "targets": {"p95": "900ms", "minRps": 5}}
]}`)

	out := r.gf("checks")

	assert.Contains(t, out, "Warning: NFR 'latency-only' has no minRps target: a script that sleeps between requests can keep latency low by sending little load.\n")
	assert.NotContains(t, out, "NFR 'with-rps'")
}

func TestStatus_OnDesign_ShowsTheDesignsProblems_AndTheHandover(t *testing.T) {
	r := newRepo(t)
	r.toDesign("new-feature")
	v := r.status()
	assert.Empty(t, v.Current.DesignProblems, "no attempt failed yet")
	assert.Nil(t, v.Work.Design, "no design accepted yet")

	r.writeArtifact("Wallets.")
	r.write(r.designFile(), strings.Replace(walletDesignJSON(t), `"invariants": ["balance never goes below zero", "a transaction is applied at most once"]`, `"invariants": []`, 1))
	r.submitAsAgent("--passed")

	v = r.expect("design", progress.AssignmentOpen, 1, 3)
	assert.Equal(t, []string{"aggregate Wallet has no invariant"}, v.Current.DesignProblems)
	assert.Contains(t, r.gf("status"), "Design problems (design-valid):\n  - aggregate Wallet has no invariant\n")

	r.designAwaitsApproval(latencyProposal)
	v = r.status()
	assert.Equal(t, progress.NextOwner, v.Next.Actor)
	assert.Equal(t, []string{"gf nfrs adopt", "gf approve", `gf reject "<feedback>"`}, v.Next.Commands)
	assert.Contains(t, v.Next.Message, "(its `## Decisions for the owner` section, if any)")
	assert.Contains(t, v.Next.Message, "adopt them with `gf nfrs adopt`, commit .gofast/nfrs.json, then run `gf approve`")

	r.gf("nfrs", "adopt", "--yes")
	v = r.status()
	assert.Equal(t, []string{"gf approve", `gf reject "<feedback>"`}, v.Next.Commands, "nothing left to adopt")
}

func TestStatus_OnSpecify_ShowsTheAcceptedDesign_AndTheSlicesNeedingSpecs(t *testing.T) {
	r := newRepo(t)
	r.toDesign("new-feature")
	r.designAwaitsApproval("")
	r.gf("approve")

	v := r.expect("specify", progress.AssignmentOpen, 0, 3)
	dir := v.Work.ArtifactDir
	assert.Equal(t, &progress.DesignView{
		Visit: 1, Artifact: dir + "design-v1.md", File: dir + "design-v1.json", Aggregates: 1, Commands: 1,
		Slices: map[string]int{"added": 3},
	}, v.Work.Design)
	assert.Equal(t, []string{"PostTransaction", "PublishWalletDebited"}, v.Current.SlicesNeedingSpecs)
	assert.Equal(t, []string{"artifact-written", "skeletons-only", "specs-fail", "specs-cover-slices"}, v.Current.Checks)
	out := r.gf("status")
	assert.Contains(t, out, "Design (visit 1): "+dir+"design-v1.md, "+dir+"design-v1.json\n  1 aggregates, 1 commands, slices: 3 added\n")
	assert.Contains(t, out, "Slices needing specs (a new Test<SliceName>… test each): PostTransaction, PublishWalletDebited\n")
}

// The wallet's posting code: what the design's slices are about. Specify
// can only write tests, so the stub exists before the work starts.
const (
	postingStub = `package server

import "errors"

// ErrInsufficientFunds rejects a debit larger than the balance.
var ErrInsufficientFunds = errors.New("insufficient funds")

// PostTransaction applies a transaction to a wallet's balance.
func PostTransaction(balance, amount int) (int, error) { return balance, nil }

// PublishWalletDebited reports whether posting amount publishes WalletDebited.
func PublishWalletDebited(amount int) bool { return false }
`
	// postingV1 applies transactions and publishes debits, but lets the
	// balance go below zero.
	postingV1 = `package server

import "errors"

// ErrInsufficientFunds rejects a debit larger than the balance.
var ErrInsufficientFunds = errors.New("insufficient funds")

// PostTransaction applies a transaction to a wallet's balance.
func PostTransaction(balance, amount int) (int, error) { return balance + amount, nil }

// PublishWalletDebited reports whether posting amount publishes WalletDebited.
func PublishWalletDebited(amount int) bool { return amount < 0 }
`
	// postingV2 also keeps the balance from going below zero.
	postingV2 = `package server

import "errors"

// ErrInsufficientFunds rejects a debit larger than the balance.
var ErrInsufficientFunds = errors.New("insufficient funds")

// PostTransaction applies a transaction to a wallet's balance.
func PostTransaction(balance, amount int) (int, error) {
	if balance+amount < 0 {
		return balance, ErrInsufficientFunds
	}
	return balance + amount, nil
}

// PublishWalletDebited reports whether posting amount publishes WalletDebited.
func PublishWalletDebited(amount int) bool { return amount < 0 }
`
	postSpec = `package server

import "testing"

func TestPostTransaction_AppliesTheAmount(t *testing.T) {
	if got, err := PostTransaction(10, 5); err != nil || got != 15 {
		t.Fatalf("PostTransaction(10, 5) = %d, %v; want 15, nil", got, err)
	}
}
`
	publishSpec = `package server

import "testing"

func TestPublishWalletDebited_OnADebit(t *testing.T) {
	if !PublishWalletDebited(-5) {
		t.Fatal("a debit publishes WalletDebited")
	}
}
`
	overdraftSpec = `package server

import (
	"errors"
	"testing"
)

func TestPostTransaction_RejectsAnOverdraft(t *testing.T) {
	if _, err := PostTransaction(10, -20); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
}
`
	publishNotOnDepositSpec = `package server

import "testing"

func TestPublishWalletDebited_NotOnADeposit(t *testing.T) {
	if PublishWalletDebited(5) {
		t.Fatal("a deposit publishes nothing")
	}
}
`
	// overdraftIntegration is run only by the integration command.
	overdraftIntegration = "//go:build integration\n\n" + `package server

import (
	"errors"
	"testing"
)

func TestWallet_IntegrationNeverGoesBelowZero(t *testing.T) {
	if _, err := PostTransaction(10, -20); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("err = %v, want ErrInsufficientFunds", err)
	}
}
`
)

// newDesignRepo is newRepo's module with the wallet's posting stub and an
// integration command that runs the unit tests.
func newDesignRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	r.write("server/posting.go", postingStub)
	r.configureChecks()
	return r
}

// specifyPasses writes specs and passes specify, waiting for approval.
func (r *repo) specifyPasses(specs map[string]string) string {
	r.t.Helper()
	for name, src := range specs {
		r.write(name, src)
	}
	r.writeArtifact("Specs for the design's slices.")
	out := r.submitAsAgent("--passed")
	r.expect("specify", progress.AssignmentAwaitingApproval, r.status().Current.AttemptsUsed, 3)
	return out
}

// 1. New feature on path v1 (started before this version of gf): design's
// first structured design is invalid, specify first misses a slice, and the
// work completes on its original path, without stress testing.
func TestNewFeature_EndToEnd(t *testing.T) {
	r := newDesignRepo(t)
	v := r.toDesign("new-feature")
	assert.Equal(t, []string{"discovery", "design", "specify", "implement", "review", "integration-testing", "stress-testing"},
		stagesOf(v.Work.Stages))
	assert.Equal(t, "human", v.Current.Gate)
	assert.Equal(t, []string{"artifact-written", "design-valid", "nfr-proposals-valid"}, v.Current.Checks)

	// Design writes artifacts only.
	r.write("server/posting.go", postingV1)
	r.writeArtifact("Wallets.")
	r.write(r.designFile(), walletDesignJSON(t))
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "Changed files outside the 'design' write scope: server/posting.go.")
	r.write("server/posting.go", postingStub)

	// An invalid structured design fails design-valid, using an attempt.
	r.write(r.designFile(), `{"boundedContext": "wallets", "aggregates": [`)
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ design-valid: "+r.designFile()+" has 1 problem: not valid JSON for gf")
	r.expect("design", progress.AssignmentOpen, 2, 3)

	r.designAwaitsApproval("")
	assert.Nil(t, r.status().Work.NFRProposals)
	r.gf("approve")

	// Specify must cover both added command and automation slices.
	v = r.expect("specify", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"PostTransaction", "PublishWalletDebited"}, v.Current.SlicesNeedingSpecs)
	r.write("server/posting_test.go", postSpec)
	r.writeArtifact("PostTransaction only.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ specs-cover-slices: no spec written in this stage covers 1 slice: PublishWalletDebited (TestPublishWalletDebited…)")
	r.expect("specify", progress.AssignmentOpen, 1, 3)
	out = r.specifyPasses(map[string]string{"server/publish_test.go": publishSpec})
	assert.Contains(t, out, "✓ specs-fail\n✓ specs-cover-slices\n")
	r.gf("approve")

	r.expect("implement", progress.AssignmentOpen, 0, 3)
	r.write("server/posting.go", postingV1)
	r.writeArtifact("PostTransaction and PublishWalletDebited.")
	assert.Contains(t, r.submitAsAgent("--passed"), "'implement' accepted.")

	for _, stage := range []string{"review", "integration-testing"} {
		r.expect(stage, progress.AssignmentOpen, 0, 3)
		r.workOnStage()
		r.submitAsAgent(r.passed()...)
	}
	assert.Nil(t, r.status().Work, "work completed")
	_, err := os.Stat(filepath.Join(r.dir, ".gofast", "nfrs.json"))
	assert.ErrorIs(t, err, os.ErrNotExist, "no NFRs touched")

	types := r.eventTypes()
	assert.Equal(t, "WorkCompleted", types[len(types)-1])
	assert.Equal(t, 0, count(types, "FixerEntered"))
}

// 2. Modify behaviour: discovery must state both behaviours; design
// proposes an NFR the owner adopts, which then makes stress testing
// mandatory.
func TestModifyBehaviour_EndToEnd_AnAdoptedNFRForcesStressTesting(t *testing.T) {
	withFakeK6(t)
	r := newDesignRepo(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	r.write("server/wallet.go", fmt.Sprintf(walletServer, fastWallet))
	r.write("cmd/walletd/main.go", walletMain)
	r.write(".gofast/checks.json", fmt.Sprintf(`{"integration": {"command": ["go", "test", "./..."]}, "timeout": "2m",
		"stress": {"start": ["go", "run", "./cmd/walletd", "-addr", %q], "ready": "http://%s/healthz", "baseUrl": "http://%s", "startTimeout": "60s", "timeout": "2m"}}`,
		addr, addr, addr))
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")

	// Discovery must state the current and the expected behaviour.
	r.startV1("modify-behaviour", "A wallet applies debits")
	v := r.expect("discovery", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "behaviour-delta"}, v.Current.Checks)
	r.write(v.Current.Artifact, "# discovery\n\n## Current behaviour\n\nPosting ignores the amount.\n")
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ behaviour-delta: "+v.Current.Artifact+": no '## Expected behaviour' section")
	r.expect("discovery", progress.AssignmentOpen, 1, 3)
	r.write(v.Current.Artifact, "# discovery\n\n## Current behaviour\n\nPosting ignores the amount.\n\n## Expected behaviour\n\nPosting applies it, and a debit publishes WalletDebited.\n")
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ behaviour-delta")
	r.gf("approve")

	// Design changes the slices and proposes an NFR on posting.
	r.expect("design", progress.AssignmentOpen, 0, 3)
	r.writeArtifact("Wallets, changed.\n\n## Decisions for the owner\n\nNone.")
	r.write(r.designFile(), strings.ReplaceAll(walletDesignJSON(t), `"change": "added"`, `"change": "changed"`))
	proposal := `{"nfrs": [{"id": "wallet-latency", "areas": ["server/**"], "operation": "POST /wallets/{id}/transactions",
  "load": {"vus": 2, "duration": "1s"}, "targets": {"p95": "40ms", "maxErrorRate": 0}}]}`
	r.write(r.proposalsFile(), proposal)
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ nfr-proposals-valid")
	v = r.expect("design", progress.AssignmentAwaitingApproval, 0, 3)
	require.NotNil(t, v.Work.NFRProposals)
	assert.Equal(t, []string{"wallet-latency"}, v.Work.NFRProposals.Added)

	// The owner adopts the NFR, commits it, and approves.
	assert.Contains(t, r.gf("nfrs", "adopt", "--yes"), "Adopted 1 NFR change into .gofast/nfrs.json.")
	r.git("add", ".gofast/nfrs.json")
	r.git("commit", "-q", "-m", "Adopt the design's NFRs")
	assert.NotContains(t, r.gf("checks"), "nfrs.json has uncommitted changes")
	r.gf("approve")

	r.expect("specify", progress.AssignmentOpen, 0, 3)
	r.specifyPasses(map[string]string{"server/posting_test.go": postSpec, "server/publish_test.go": publishSpec})
	r.gf("approve")
	r.expect("implement", progress.AssignmentOpen, 0, 3)
	r.write("server/posting.go", postingV1)
	r.writeArtifact("Posting applies the amount.")
	r.submitAsAgent("--passed")

	// The change touches the adopted NFR's areas: review can't skip stress.
	v = r.expect("review", progress.AssignmentOpen, 0, 3)
	require.Len(t, v.Current.Stress.Mandatory, 1)
	assert.Equal(t, "wallet-latency", v.Current.Stress.Mandatory[0].ID)
	r.workOnStage()
	res := r.runWithoutTerminal(asAgent, "submit", "--passed", "--stress", "skip", skipStress)
	require.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "Stress testing is mandatory: the change touches the areas of wallet-latency (server/**).")
	assert.Contains(t, r.submitAsAgent("--passed", "--stress", "include", includeStress), "(mandatory: wallet-latency)")

	r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	r.workOnStage()
	r.submitAsAgent("--passed")
	r.expect("stress-testing", progress.AssignmentOpen, 0, 3)
	r.workOnStage()
	r.writeScript(walletScript)
	assert.Contains(t, r.submitAsAgent("--passed"), "✓ nfrs-met\n'stress-testing' accepted.")
	assert.Nil(t, r.status().Work, "work completed")
	assert.NotContains(t, strings.Join(r.eventTypes(), " "), "NFR", "adopting records nothing")
}

// 3. Integration fails because the model missed an invariant: the fixer
// sends the work back to design, whose second visit the owner approves,
// and specify then covers the new design.
func TestNewFeature_TheModelMissesAnInvariant_TheFixerSendsItToDesign(t *testing.T) {
	r := newDesignRepo(t)
	r.write("server/posting_integration_test.go", overdraftIntegration)
	r.write(".gofast/checks.json", `{"integration": {"command": ["go", "test", "-tags=integration", "./..."]}, "timeout": "2m"}`)
	r.toDesign("new-feature")
	workID := r.status().Work.ID

	// Design v1 misses "balance never goes below zero".
	r.writeArtifact("Wallets.")
	r.write(r.designFile(), strings.Replace(walletDesignJSON(t), `"balance never goes below zero", `, "", 1))
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.specifyPasses(map[string]string{"server/posting_test.go": postSpec, "server/publish_test.go": publishSpec})
	r.gf("approve")
	r.write("server/posting.go", postingV1)
	r.writeArtifact("Posting.")
	r.submitAsAgent("--passed")
	r.expect("review", progress.AssignmentOpen, 0, 3)
	r.workOnStage()
	r.submitAsAgent(reviewPass...)

	// Integration finds an overdraft: the fixer may route to design.
	r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	r.workOnStage()
	assert.Contains(t, r.submitAsAgent("--passed"), "the work goes to the fixer")
	v := r.expect("fixer", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"design", "specify", "implement"}, v.Current.Fixer.Targets)
	r.writeArtifact("Root cause: the Wallet aggregate has no invariant keeping the balance at or above zero, so nothing rejects an overdraft.")
	out := r.submitAsAgent("--passed", "--route", "design", "the model misses an invariant")
	assert.Contains(t, out, "'fixer' accepted: the work returns to 'design' (the model misses an invariant).")

	// Design's second visit, on its human gate.
	v = r.expect("design", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, "human", v.Current.Gate)
	assert.Equal(t, ".gofast/works/"+workID+"/fixer-v1.md", v.Current.ReworkOf.FixerArtifact)
	assert.Equal(t, 1, v.Work.Design.Visit, "the accepted design is still v1")
	assert.Equal(t, ".gofast/works/"+workID+"/design-v2.json", r.designFile())
	r.designAwaitsApproval("")
	r.gf("approve")

	// Specify's second visit covers design v2's slices with new specs.
	v = r.expect("specify", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, 2, v.Work.Design.Visit)
	r.write("server/overdraft_test.go", overdraftSpec)
	r.writeArtifact("The overdraft.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ specs-cover-slices: no spec written in this stage covers 1 slice: PublishWalletDebited")
	out = r.specifyPasses(map[string]string{"server/publish_deposit_test.go": publishNotOnDepositSpec})
	assert.Contains(t, out, "✓ specs-fail\n✓ specs-cover-slices\n")
	r.gf("approve")

	r.expect("implement", progress.AssignmentOpen, 0, 3)
	r.write("server/posting.go", postingV2)
	r.writeArtifact("Posting rejects overdrafts.")
	assert.Contains(t, r.submitAsAgent("--passed"), "'implement' accepted.")
	for _, stage := range []string{"review", "integration-testing"} {
		v = r.expect(stage, progress.AssignmentOpen, 0, 3)
		assert.Equal(t, 2, v.Current.Visit)
		r.workOnStage()
		r.submitAsAgent(r.passed()...)
	}
	assert.Nil(t, r.status().Work, "work completed")

	types := r.eventTypes()
	assert.Equal(t, 1, count(types, "ReworkStarted"))
	assert.Equal(t, "WorkCompleted", types[len(types)-1])
}

func stagesOf(stages []progress.StageView) []string {
	var names []string
	for _, s := range stages {
		names = append(names, s.Stage)
	}
	return names
}
