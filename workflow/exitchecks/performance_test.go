package exitchecks_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

var walletLatency = Must(domain.NewNFRID("wallet-latency"))

// visitOn runs the checks of a stage visit of improve-performance work that
// targets wallet-latency.
func (m *stressModule) visitOn(stage domain.Stage, config exitchecks.Config, path string) map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	changed, _, err := worktree.OpenBaselines(m.root).ChangedSince(AssignmentOn(stage))
	require.NoError(m.t, err)
	runner := exitchecks.Runner{Root: m.root, Config: config, Env: []string{"PATH=" + path, "HOME=" + os.Getenv("HOME")}}
	results, err := runner.Run(exitchecks.Visit{
		Assignment: AssignmentOn(stage), Work: WorkID, Path: Must(domain.PathFor(domain.WorkTypeImprovePerformance)),
		Stage: stage, Visit: Visit1, Attempt: Attempt(1), Changed: changed, FirstAssignment: AssignmentOn(domain.StageDiscovery),
		TargetNFRs: []domain.NFRID{walletLatency},
	})
	require.NoError(m.t, err)
	byCheck := map[domain.ExitCheck]domain.CheckResult{}
	for _, r := range results {
		byCheck[r.Check()] = r
	}
	return byCheck
}

const diagnosis = "# Measure\n\n## Bottleneck\n\nThe handler sleeps 40ms per transaction: see profiles/cpu.pprof.\n"

// onMeasure begins measure, with its diagnosis and a profile as evidence.
func (m *stressModule) onMeasure(artifact string) {
	m.begin(domain.StageMeasure)
	m.write(domain.ArtifactFor(WorkID, domain.StageMeasure, Visit1).String(), artifact)
	m.write(domain.ArtifactDir(WorkID).String()+"/profiles/cpu.pprof", "profile")
}

func TestMeasure_RecordsTheBaselineOfTheTargets(t *testing.T) {
	m := newStressModule(t, `{"p95": "5ms"}`)
	m.onMeasure(diagnosis)
	m.script("wallet-latency", transactionScript)

	results := m.visitOn(domain.StageMeasure, m.config(t, "-delay", "40ms"), withFakeK6(t))

	passes(t, results[domain.CheckStressScriptsValid])
	passes(t, results[domain.CheckBaselineRecorded])
	passes(t, results[domain.CheckDiagnosisWritten])
	judged, kept, err := exitchecks.MeasuredBaseline(m.root, AssignmentOn(domain.StageMeasure), []domain.NFRID{walletLatency})
	require.NoError(t, err)
	require.Len(t, judged, 1)
	assert.False(t, judged[0].Met(), "a slow handler misses the target")
	assert.Equal(t, "wallet-latency", kept[0].NFR)
}

func TestMeasure_WithoutAScriptForATarget_RecordsNoBaseline(t *testing.T) {
	m := newStressModule(t, `{"p95": "5ms"}`)
	m.onMeasure(diagnosis)

	results := m.visitOn(domain.StageMeasure, m.config(t), withFakeK6(t))

	fails(t, results[domain.CheckStressScriptsValid], "no script for wallet-latency, which the work targets")
	fails(t, results[domain.CheckBaselineRecorded], "not measured")
}

func TestDiagnosisWritten(t *testing.T) {
	check := func(artifact string) domain.CheckResult {
		t.Helper()
		m := newStressModule(t, `{"p95": "5ms"}`)
		m.onMeasure(artifact)
		m.script("wallet-latency", transactionScript)
		return m.visitOn(domain.StageMeasure, m.config(t), withFakeK6(t))[domain.CheckDiagnosisWritten]
	}
	passes(t, check(diagnosis))
	passes(t, check("## Bottleneck\n\nJSON encoding dominates (.gofast/works/w1/profiles/cpu.pprof).\n"))
	fails(t, check("# Measure\n\nSlow.\n"), "has no '## Bottleneck' section")
	fails(t, check("## Bottleneck\n\n## Next\n"), "the '## Bottleneck' section of .gofast/works/w1/measure-v1.md is empty")
	fails(t, check("## Bottleneck\n\nThe handler is slow.\n"), "references no file in .gofast/works/w1/")
}

// After optimize, stress testing judges the targets and shows the change
// against the baseline, for information.
func TestStressTesting_OnImprovePerformance_ComparesAgainstTheBaseline(t *testing.T) {
	m := newStressModule(t, `{"p95": "500ms"}`)
	require.NoError(t, exitchecks.WriteBaseline(m.root, WorkID, []exitchecks.StressResult{{NFR: "wallet-latency", P95Ms: 212, P99Ms: 300, RPS: 10, Requests: 100}}))
	m.onStress(false)
	m.script("wallet-latency", transactionScript)

	results := m.visitOn(domain.StageStressTesting, m.config(t), withFakeK6(t))
	passes(t, results[domain.CheckNFRsMet])
	log, err := os.ReadFile(m.root + "/" + exitchecks.LogPath(AssignmentOn(domain.StageStressTesting), Attempt(1)))
	require.NoError(t, err)
	assert.Contains(t, string(log), "=== nfrs-met passed: against the baseline: wallet-latency: p95 212ms → ")
	assert.Contains(t, string(log), "(target 500ms)")

	slow := newStressModule(t, `{"p95": "5ms"}`)
	require.NoError(t, exitchecks.WriteBaseline(slow.root, WorkID, []exitchecks.StressResult{{NFR: "wallet-latency", P95Ms: 212, P99Ms: 300, RPS: 10, Requests: 100}}))
	slow.onStress(false)
	slow.script("wallet-latency", transactionScript)
	fails(t, slow.visitOn(domain.StageStressTesting, slow.config(t, "-delay", "40ms"), withFakeK6(t))[domain.CheckNFRsMet],
		"wallet-latency: p95 ", "against the baseline: wallet-latency: p95 212ms → ")
}
