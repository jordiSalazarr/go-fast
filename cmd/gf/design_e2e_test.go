package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// The design stage end to end: new-feature and modify-behaviour work, NFR
// proposals the owner adopts, and rework back into design.

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

// toDesign commits the repository, starts work of a designed type and takes
// it through discovery, to design's first visit.
func (r *repo) toDesign(workType string) progress.View {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	r.gf("start", "--type", workType, "The server listens on 8080")
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
	r.expect("design", progress.AssignmentAwaitingApproval, 0, 3)
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
