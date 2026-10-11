package exitchecks_test

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

var (
	buildOnce sync.Once
	binDir    string // holds k6 (the fake) and walletserver
	buildErr  error
)

// testBinaries builds the fake k6 and the wallet server once per test run.
func testBinaries(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		binDir, buildErr = os.MkdirTemp("", "gf-stress-bin-")
		if buildErr != nil {
			return
		}
		for name, pkg := range map[string]string{"k6": "./testdata/fakek6", "walletserver": "./testdata/walletserver"} {
			if out, err := exec.Command("go", "build", "-o", filepath.Join(binDir, name), pkg).CombinedOutput(); err != nil {
				buildErr = fmt.Errorf("build %s: %v: %s", pkg, err, out)
				return
			}
		}
	})
	require.NoError(t, buildErr)
	return binDir
}

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().String()
}

const walletArea = "server/wallet/**"

// stressModule is a module whose NFR covers server/wallet/, on the stress
// testing stage of a work that started before anything changed.
type stressModule struct {
	*module
	addr string
}

func newStressModule(t *testing.T, targets string) *stressModule {
	t.Helper()
	m := &stressModule{module: newModule(t), addr: freePort(t)}
	m.write(exitchecks.NFRsPath, `{"nfrs": [{"id": "wallet-latency", "areas": ["`+walletArea+`"], "operation": "POST /wallets/{id}/transactions",
		"load": {"vus": 2, "duration": "1s"}, "targets": `+targets+`}]}`)
	m.git("add", "-A")
	m.git("-c", "user.name=T", "-c", "user.email=t@x", "commit", "-q", "-m", "nfrs")
	m.begin(domain.StageDiscovery) // the work's first stage visit
	return m
}

// config runs the wallet server with these flags.
func (m *stressModule) config(t *testing.T, flags ...string) exitchecks.Config {
	start := append([]string{filepath.Join(testBinaries(t), "walletserver"), "-addr", m.addr}, flags...)
	return exitchecks.Config{Timeout: time.Minute, Stress: &exitchecks.Stress{
		Start: start, Ready: "http://" + m.addr + "/healthz", BaseURL: "http://" + m.addr,
		StartTimeout: 10 * time.Second, Timeout: time.Minute,
	}}
}

// onStress changes the wallet code (or not) and begins stress testing.
func (m *stressModule) onStress(touchWallet bool) {
	if touchWallet {
		m.write("server/wallet/post.go", "package wallet\n")
	}
	m.begin(domain.StageStressTesting)
	m.writeArtifact(domain.StageStressTesting)
}

func (m *stressModule) script(id, src string) {
	m.write(domain.StressScriptFor(WorkID, Must(domain.NewNFRID(id))).String(), src)
}

const transactionScript = "import http from 'k6/http';\n// fake-k6: POST /wallets/7/transactions\nexport default function () {\n  http.post(`${__ENV.BASE_URL}/wallets/7/transactions`, '{}');\n}\n"

// stressCheck runs stress testing's checks with this PATH and extra
// environment.
func (m *stressModule) stressCheck(config exitchecks.Config, path string, env ...string) map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	changed, _, err := worktree.OpenBaselines(m.root).ChangedSince(AssignmentOn(domain.StageStressTesting))
	require.NoError(m.t, err)
	runner := exitchecks.Runner{Root: m.root, Config: config, Env: append([]string{"PATH=" + path, "HOME=" + os.Getenv("HOME")}, env...)}
	results, err := runner.Run(exitchecks.Visit{
		Assignment: AssignmentOn(domain.StageStressTesting), Work: WorkID, Stage: domain.StageStressTesting, Visit: Visit1, Attempt: Attempt(1),
		Changed: changed, FirstAssignment: AssignmentOn(domain.StageDiscovery),
	})
	require.NoError(m.t, err)
	byCheck := map[domain.ExitCheck]domain.CheckResult{}
	for _, r := range results {
		byCheck[r.Check()] = r
	}
	require.Len(m.t, byCheck, 3)
	return byCheck
}

func withFakeK6(t *testing.T) string {
	return testBinaries(t) + string(os.PathListSeparator) + os.Getenv("PATH")
}

func runDir(m *stressModule) string {
	return filepath.Join(m.root, filepath.FromSlash(exitchecks.StressRunDir(AssignmentOn(domain.StageStressTesting), Attempt(1))))
}

func TestStress_AFastService_MeetsTheNFR_MeasuredByGf(t *testing.T) {
	m := newStressModule(t, `{"p95": "500ms", "maxErrorRate": 0}`)
	m.onStress(true)
	m.script("wallet-latency", transactionScript)

	results := m.stressCheck(m.config(t), withFakeK6(t), "K6_RPS=1", "K6_VUS=1")

	passes(t, results[domain.CheckStressScriptsValid])
	passes(t, results[domain.CheckNFRsMet])
	args, err := os.ReadFile(filepath.Join(runDir(m), "wallet-latency.json.gz.args"))
	require.NoError(t, err)
	for _, want := range []string{"--no-thresholds", "--summary-mode\ndisabled", "--vus\n2", "--duration\n1s", "--env\nBASE_URL=http://" + m.addr,
		"--out\njson=" + filepath.Join(runDir(m), "wallet-latency.json.gz"), ".gofast/works/w1/stress/wallet-latency.js"} {
		assert.Contains(t, string(args), want)
	}
	assert.NotContains(t, string(args), "env K6_", "K6_* variables could set options behind the command line's back")
	for _, f := range []string{"wallet-latency.json.gz", "wallet-latency.k6.log", "wallet-latency.service.log"} {
		assert.FileExists(t, filepath.Join(runDir(m), f))
	}
	log, err := os.ReadFile(filepath.Join(m.root, filepath.FromSlash(exitchecks.LogPath(AssignmentOn(domain.StageStressTesting), Attempt(1)))))
	require.NoError(t, err)
	assert.Contains(t, string(log), "measured wallet-latency: p95 ")
	assert.False(t, answering("http://"+m.addr+"/healthz"), "the service is stopped")

	attempt, recorded, ok, err := exitchecks.ReadStressResults(m.root, AssignmentOn(domain.StageStressTesting))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, attempt)
	require.Len(t, recorded, 1)
	assert.Equal(t, "wallet-latency", recorded[0].NFR)
	assert.True(t, recorded[0].Met)
	assert.Equal(t, "p95 ≤ 500ms, error rate ≤ 0%", recorded[0].Targets)
	assert.Positive(t, recorded[0].Requests)
}

func TestStress_ASlowService_MissesTheNFR_WithMeasuredAgainstTarget(t *testing.T) {
	m := newStressModule(t, `{"p95": "5ms"}`)
	m.onStress(true)
	m.script("wallet-latency", transactionScript)

	results := m.stressCheck(m.config(t, "-delay", "40ms"), withFakeK6(t))

	passes(t, results[domain.CheckStressScriptsValid])
	fails(t, results[domain.CheckNFRsMet], "wallet-latency: p95 ", "ms > 5ms", "(log: .gofast/runtime/checks/")
	_, recorded, ok, err := exitchecks.ReadStressResults(m.root, AssignmentOn(domain.StageStressTesting))
	require.NoError(t, err)
	require.True(t, ok)
	assert.False(t, recorded[0].Met)
	assert.Len(t, recorded[0].Missed, 1)
}

func TestStress_AServiceThatNeverGetsReady_MissesTheNFR(t *testing.T) {
	m := newStressModule(t, `{"p95": "500ms"}`)
	m.onStress(true)
	m.script("wallet-latency", transactionScript)

	results := m.stressCheck(m.config(t, "-crash"), withFakeK6(t))

	passes(t, results[domain.CheckStressScriptsValid])
	fails(t, results[domain.CheckNFRsMet], "wallet-latency: the service exited before it was ready", "wallet-latency.service.log")
}

// Everything the agent or the owner must provide fails stress-scripts-valid,
// so the stage tries again rather than sending the work to the fixer; the
// NFRs are then not measured.
func TestStress_ScriptsNotValid(t *testing.T) {
	cases := map[string]struct {
		touchWallet bool
		scripts     map[string]string
		noConfig    bool
		noK6        bool
		noNFRs      bool
		want        string
	}{
		"no k6 on PATH":              {touchWallet: true, scripts: map[string]string{"wallet-latency": transactionScript}, noK6: true, want: "k6 is required for stress testing. Install it (https://k6.io) and run `gf checks`."},
		"no stress configuration":    {touchWallet: true, scripts: map[string]string{"wallet-latency": transactionScript}, noConfig: true, want: "No stress configuration in the gofast config."},
		"no NFRs":                    {noNFRs: true, want: "No NFRs in .gofast/nfrs.json, so there is nothing to stress test against."},
		"a mandatory NFR unscripted": {touchWallet: true, want: "no script for wallet-latency, which this change makes mandatory: write .gofast/works/w1/stress/wallet-latency.js"},
		"no script at all":           {want: "no stress script: write one per NFR to exercise, at .gofast/works/w1/stress/<nfr-id>.js"},
		"a script for no NFR":        {scripts: map[string]string{"checkout": transactionScript}, want: ".gofast/works/w1/stress/checkout.js names no NFR in .gofast/nfrs.json"},
		"a script with thresholds": {touchWallet: true, scripts: map[string]string{"wallet-latency": transactionScript + "export const options = { thresholds: { http_req_duration: ['p(95)<1'] } };\n"},
			want: "wallet-latency.js sets options.thresholds (gf sets the load from the NFR)"},
		"a script that fails under k6": {touchWallet: true, scripts: map[string]string{"wallet-latency": "// fake-k6: exit 107\nexport default function () { thisIsNotDefined(); }\n"},
			want: "wallet-latency.js failed under k6 (failed (exit status 107)): level=error"},
		"a script without the NFR's operation": {touchWallet: true, scripts: map[string]string{"wallet-latency": "// fake-k6: POST /healthz\nexport default function () {}\n"},
			want: "wallet-latency.js sent no POST /wallets/{id}/transactions requests under load"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := newStressModule(t, `{"p95": "500ms"}`)
			if c.noNFRs {
				m.remove(exitchecks.NFRsPath)
			}
			m.onStress(c.touchWallet)
			for id, src := range c.scripts {
				m.script(id, src)
			}
			config := m.config(t)
			if c.noConfig {
				config.Stress = nil
			}
			path := withFakeK6(t)
			if c.noK6 {
				path = "/usr/bin:/bin"
			}

			results := m.stressCheck(config, path)

			fails(t, results[domain.CheckStressScriptsValid], c.want)
			fails(t, results[domain.CheckNFRsMet], "not measured: the stress scripts aren't valid")
		})
	}
}

// gf would otherwise measure whatever already listens there.
func TestStress_SomethingAlreadyAnswersAtTheReadyURL_IsNotMeasured(t *testing.T) {
	m := newStressModule(t, `{"p95": "500ms"}`)
	m.onStress(true)
	m.script("wallet-latency", transactionScript)
	l, err := net.Listen("tcp", m.addr)
	require.NoError(t, err)
	server := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	go server.Serve(l)
	defer server.Close()

	results := m.stressCheck(m.config(t), withFakeK6(t))

	fails(t, results[domain.CheckStressScriptsValid], "something already answers at http://"+m.addr+"/healthz before gf started the service")
}

func answering(url string) bool {
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// With k6 installed, the same run goes through the real thing.
func TestStress_WithRealK6(t *testing.T) {
	k6, err := exec.LookPath("k6")
	if err != nil {
		t.Skip("k6 is not installed (https://k6.io); the fake k6 covers the flow")
	}
	m := newStressModule(t, `{"p95": "500ms", "maxErrorRate": 0, "minRps": 1}`)
	m.onStress(true)
	m.script("wallet-latency", "import http from 'k6/http';\n\nexport function setup() { http.get(`${__ENV.BASE_URL}/healthz`); }\n\n"+
		"export default function () {\n  http.post(http.url`${__ENV.BASE_URL}/wallets/${7}/transactions`, '{}');\n}\n")

	results := m.stressCheck(m.config(t), filepath.Dir(k6)+string(os.PathListSeparator)+os.Getenv("PATH"))

	passes(t, results[domain.CheckStressScriptsValid])
	passes(t, results[domain.CheckNFRsMet])
	k6log, _ := os.ReadFile(filepath.Join(runDir(m), "wallet-latency.k6.log"))
	assert.False(t, strings.Contains(string(k6log), "level=error"), string(k6log))
}
