package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// Stress testing end to end: a Go module whose server posts wallet
// transactions, and an NFR on that operation. Every scenario runs with a
// fake k6 (a test double that makes the requests a script declares and
// writes k6's output), so the flow is covered without k6; the first runs
// again with real k6 when it is installed.

const (
	// walletServer serves the wallet; posting a transaction takes
	// transactionDelay.
	walletServer = `package server

import (
	"net/http"
	"time"
)

// transactionDelay is how long posting a transaction takes.
var transactionDelay = %s

// Handler serves the wallet API.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /wallets/{id}/transactions", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(transactionDelay)
		w.WriteHeader(http.StatusCreated)
	})
	return mux
}
`
	walletMain = `package main

import (
	"flag"
	"net/http"

	"example.com/server/server"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	flag.Parse()
	_ = http.ListenAndServe(*addr, server.Handler())
}
`
	slowWallet = "80 * time.Millisecond"
	fastWallet = "time.Duration(0)"

	// walletScript works under k6 and, through its comment, the fake.
	walletScript = "import http from 'k6/http';\n\n" +
		"// fake-k6: POST /wallets/w7/transactions\n" +
		"export default function () {\n" +
		"  http.post(`${__ENV.BASE_URL}/wallets/w7/transactions`, JSON.stringify({ amount: 5 }));\n" +
		"}\n"

	skipStress    = "nothing in this change runs under load"
	includeStress = "the change touches the server's request path"
)

var (
	fakeK6Once sync.Once
	fakeK6Dir  string
	fakeK6Err  error
)

// withFakeK6 puts the fake k6 first on PATH for the rest of the test.
func withFakeK6(t *testing.T) {
	t.Helper()
	fakeK6Once.Do(func() {
		if fakeK6Dir, fakeK6Err = os.MkdirTemp("", "gf-fake-k6-"); fakeK6Err != nil {
			return
		}
		out, err := exec.Command("go", "build", "-o", filepath.Join(fakeK6Dir, "k6"), "../../workflow/exitchecks/testdata/fakek6").CombinedOutput()
		if err != nil {
			fakeK6Err = fmt.Errorf("build the fake k6: %v: %s", err, out)
		}
	})
	require.NoError(t, fakeK6Err)
	t.Setenv("PATH", fakeK6Dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// withRealK6 keeps PATH as it is, or skips the test without k6.
func withRealK6(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("k6"); err != nil {
		t.Skip("k6 is not installed (https://k6.io): the fake k6 covers this flow")
	}
}

// newStressRepo is newRepo's module (Port returns 8081) with a wallet server
// in the same package, whose transactions take delay, and an NFR on posting
// them that covers areas. Everything is committed before work starts.
func newStressRepo(t *testing.T, areas, delay string) *repo {
	t.Helper()
	r := newRepo(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	r.write("server/wallet.go", fmt.Sprintf(walletServer, delay))
	r.write("cmd/walletd/main.go", walletMain)
	r.write(".gofast/checks.json", fmt.Sprintf(`{"integration": {"command": ["go", "test", "./..."]}, "timeout": "2m",
		"stress": {"start": ["go", "run", "./cmd/walletd", "-addr", %q], "ready": "http://%s/healthz", "baseUrl": "http://%s", "startTimeout": "60s", "timeout": "2m"}}`,
		addr, addr, addr))
	r.write(".gofast/nfrs.json", `{"nfrs": [{"id": "wallet-latency", "description": "Posting a transaction stays fast",
		"areas": [`+areas+`], "operation": "POST /wallets/{id}/transactions",
		"load": {"vus": 2, "duration": "1s"}, "targets": {"p95": "40ms", "maxErrorRate": 0}}]}`)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	return r
}

// toStressReview takes the work through discovery, specify and implement
// (which fixes Port in server/) to review's first visit.
func (r *repo) toStressReview() progress.View {
	r.t.Helper()
	r.gf("start", "--type", "fix-bug", "The server does not listen on 8080")
	for range 2 { // discovery, specify
		r.workOnStage()
		r.submitAsAgent("--passed")
		r.gf("approve")
	}
	r.workOnStage()
	r.submitAsAgent("--passed")
	return r.expect("review", progress.AssignmentOpen, 0, 3)
}

// writeScript writes the stress-testing agent's script for the NFR.
func (r *repo) writeScript(src string) {
	r.t.Helper()
	r.write(r.status().Work.ArtifactDir+"stress/wallet-latency.js", src)
}

// 1. The change touches the NFR's areas: review can't skip stress testing;
// it includes it, and the script meets the targets.
func TestStress_ReviewCantSkipAForcedStressTest_AndTheScriptMeetsTheNFR(t *testing.T) {
	withFakeK6(t)
	forcedStressTestPasses(t)
}

func TestStress_ReviewCantSkipAForcedStressTest_AndTheScriptMeetsTheNFR_WithRealK6(t *testing.T) {
	withRealK6(t)
	forcedStressTestPasses(t)
}

func forcedStressTestPasses(t *testing.T) {
	r := newStressRepo(t, `"server/**"`, fastWallet)
	v := r.toStressReview()
	require.NotNil(t, v.Current.Stress)
	require.Len(t, v.Current.Stress.Mandatory, 1)
	assert.Equal(t, "wallet-latency", v.Current.Stress.Mandatory[0].ID)
	assert.Equal(t, []string{`gf submit --passed --stress include "<reason>"`, `gf submit --failed "<reason>"`}, v.Next.Commands)
	r.workOnStage()

	res := r.runWithoutTerminal(asAgent, "submit", "--passed", "--stress", "skip", skipStress)
	require.Equal(t, 1, res.code)
	assert.Equal(t, "Stress testing is mandatory: the change touches the areas of wallet-latency (server/**). Submit with `gf submit --passed --stress include \"<reason>\"`. No attempt was used.\n", res.stderr)
	assert.NotContains(t, res.stdout, "Running the exit checks", "refused before any check runs")
	res = r.runWithoutTerminal(asAgent, "submit", "--passed")
	require.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "Review decides whether stress testing runs: `gf submit --passed --stress <include|skip> \"<reason>\"`.")
	r.expect("review", progress.AssignmentOpen, 0, 3)

	out := r.submitAsAgent("--passed", "--stress", "include", includeStress)
	assert.Contains(t, out, "'review' accepted, deciding to include stress testing (mandatory: wallet-latency).")

	v = r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, &progress.StressDecisionView{Decision: "include", Reason: includeStress, ForcedBy: []string{"wallet-latency"}, DecidedBy: "review"},
		v.Current.Stress.Decision)
	r.workOnStage()
	r.submitAsAgent("--passed")

	v = r.expect("stress-testing", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "stress-scripts-valid", "nfrs-met"}, v.Current.Checks)
	assert.Equal(t, ".gofast/works/"+v.Work.ID+"/stress/wallet-latency.js", v.Current.Stress.Mandatory[0].Script)
	r.workOnStage()
	r.writeScript(walletScript)
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ stress-scripts-valid\n✓ nfrs-met\n'stress-testing' accepted.")
	assert.Nil(t, r.status().Work, "work completed")

	types := r.eventTypes()
	assert.Equal(t, 1, count(types, "StressDecided"))
	assert.Equal(t, "WorkCompleted", types[len(types)-1])
}

// 2. A slow handler misses the NFR: the work goes to the fixer in
// performance mode, back to implement, and through review, integration and
// stress testing again.
func TestStress_ASlowHandlerMissesTheNFR_TheFixerSendsItToImplement(t *testing.T) {
	withFakeK6(t)
	r := newStressRepo(t, `"server/**"`, slowWallet)
	workID := r.toStressReview().Work.ID
	r.workOnStage()
	r.submitAsAgent("--passed", "--stress", "include", includeStress)
	r.workOnStage()
	r.submitAsAgent("--passed") // integration testing

	r.expect("stress-testing", progress.AssignmentOpen, 0, 3)
	r.workOnStage()
	r.writeScript(walletScript)
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ stress-scripts-valid\n✗ nfrs-met: wallet-latency: p95 ")
	assert.Contains(t, out, "ms > 40ms")
	assert.Contains(t, out, "the work goes to the fixer (no attempt used)")

	// The fixer, in performance mode, with the measurements.
	v := r.expect("fixer", progress.AssignmentOpen, 0, 3)
	require.NotNil(t, v.Current.Fixer)
	assert.Equal(t, "stress-testing", v.Current.Fixer.From)
	assert.Equal(t, "performance", v.Current.Fixer.Mode)
	assert.Contains(t, v.Current.Fixer.Reason, "nfrs-met: wallet-latency: p95 ")
	assert.Equal(t, ".gofast/runtime/checks/"+workID+"-stress-testing-1-attempt1/", v.Current.Fixer.StressRunDir)
	assert.FileExists(t, filepath.Join(r.dir, v.Current.Fixer.StressRunDir, "wallet-latency.json.gz"))
	r.writeArtifact("Bottleneck: transactionDelay sleeps 80ms per transaction (p95 above 80ms against 40ms). Implement must remove it.")
	r.submitAsAgent("--passed", "--route", "implement", "every transaction sleeps 80ms")

	// Implement's second visit removes the sleep.
	v = r.expect("implement", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, "implement", v.Current.ReworkOf.Target)
	r.write("server/wallet.go", fmt.Sprintf(walletServer, fastWallet))
	r.writeArtifact("Transactions no longer sleep.")
	r.submitAsAgent("--passed")

	// Review decides again, integration and stress testing run again.
	v = r.expect("review", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Nil(t, v.Current.Stress.Decision, "a rework clears the decision")
	r.workOnStage()
	r.submitAsAgent("--passed", "--stress", "include", includeStress)
	r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	r.workOnStage()
	r.submitAsAgent("--passed")
	v = r.expect("stress-testing", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	r.workOnStage()
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ nfrs-met\n'stress-testing' accepted.")
	assert.Nil(t, r.status().Work, "work completed")
	assert.Equal(t, 2, count(r.eventTypes(), "StressDecided"))
}

// 3. A change outside every NFR's areas: review skips stress testing, and
// the work completes after integration testing. The owner may override,
// from their own terminal only.
func TestStress_AChangeOutsideEveryNFR_ReviewSkipsIt(t *testing.T) {
	withFakeK6(t)
	r := newStressRepo(t, `"internal/ledger/**"`, fastWallet)
	v := r.toStressReview()
	assert.Empty(t, v.Current.Stress.Mandatory)
	r.workOnStage()
	out := r.submitAsAgent("--passed", "--stress", "skip", skipStress)
	assert.Contains(t, out, "'review' accepted, deciding to skip stress testing.")

	v = r.expect("integration-testing", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, progress.StageSkipped, v.Work.Stages[len(v.Work.Stages)-1].Status)
	assert.Contains(t, r.gf("status"), "Stress decision: skip, by review: "+skipStress)

	// Only the owner overrides, and only from a terminal.
	res := r.gfFails(asAgent, "stress", "include", "I want numbers")
	assert.Contains(t, res.stderr, "Only the owner can decide whether stress testing runs")
	res = r.runWithoutTerminal(nil, "stress", "include", "I want numbers")
	require.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "from their own terminal")
	out = r.gf("stress", "skip", "confirmed: no load-sensitive change")
	assert.Contains(t, out, "Stress testing is skipped: confirmed: no load-sensitive change")
	assert.Equal(t, "owner", r.status().Current.Stress.Decision.DecidedBy)

	r.workOnStage()
	r.submitAsAgent("--passed")
	assert.Nil(t, r.status().Work, "work completed without stress testing")
	assert.Equal(t, 5, count(r.eventTypes(), "AssignmentOpened"), "discovery to integration testing; no stress testing")
}

// 4. A script that sets thresholds fails stress-scripts-valid: the stage's
// own work, so it tries again instead of going to the fixer.
func TestStress_AScriptWithThresholds_IsTheStagesOwnWork(t *testing.T) {
	withFakeK6(t)
	r := newStressRepo(t, `"server/**"`, fastWallet)
	r.toStressReview()
	r.workOnStage()
	r.submitAsAgent("--passed", "--stress", "include", includeStress)
	r.workOnStage()
	r.submitAsAgent("--passed")
	r.expect("stress-testing", progress.AssignmentOpen, 0, 3)

	r.workOnStage()
	r.writeScript(walletScript + "\nexport const options = { thresholds: { http_req_duration: ['p(95)<100000'] } };\n")
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "✗ stress-scripts-valid: wallet-latency.js sets options.thresholds (gf sets the load from the NFR)")
	assert.Contains(t, out, "Attempt 1 on 'stress-testing' failed (1 of 3 attempts used)")
	v := r.expect("stress-testing", progress.AssignmentOpen, 1, 3)
	assert.Zero(t, v.Work.Reworks, "not handed to the fixer")

	r.workOnStage()
	r.writeScript(walletScript)
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "'stress-testing' accepted.")
	assert.Nil(t, r.status().Work)
}

// Without k6 the stage fails closed, telling the owner what to install.
func TestStress_WithoutK6_TheStageFailsClosed(t *testing.T) {
	t.Setenv("PATH", strings.Join([]string{filepath.Dir(goBinary(t)), "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	r := newStressRepo(t, `"server/**"`, fastWallet)
	r.toStressReview()
	r.workOnStage()
	r.submitAsAgent("--passed", "--stress", "include", includeStress)
	r.workOnStage()
	r.submitAsAgent("--passed")

	r.workOnStage()
	r.writeScript(walletScript)
	out := r.submitAsAgent("--passed")

	assert.Contains(t, out, "✗ stress-scripts-valid: k6 is required for stress testing. Install it (https://k6.io) and run `gf checks`.")
	r.expect("stress-testing", progress.AssignmentOpen, 1, 3)
}

func goBinary(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("go")
	require.NoError(t, err)
	return path
}
