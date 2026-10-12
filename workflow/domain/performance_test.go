package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

var (
	latency     = walletNFR(domain.TargetValues{P95: ptr(ms(150))})
	ledgerWrite = must(domain.NewNFR(must(domain.NewNFRID("ledger-write")), "", areas("internal/ledger/**"),
		must(domain.NewOperation("POST /ledger")), must(domain.NewLoad(10, time.Second)), must(domain.NewTargets(domain.TargetValues{P95: ptr(ms(50))}))))
	repoNFRs = []domain.NFR{latency, ledgerWrite}
	slow     = measured(domain.MeasurementValues{P95: ms(212), P99: ms(300), RPS: 240, Requests: 100})
	fast     = measured(domain.MeasurementValues{P95: ms(120), P99: ms(200), RPS: 260, Requests: 100})
)

// Improve-performance work measures, optimizes, and always stress tests.
func TestPerformancePath(t *testing.T) {
	path := must(domain.PathFor(domain.WorkTypeImprovePerformance))
	var stages []domain.Stage
	for _, s := range path.Steps() {
		stages = append(stages, s.Stage())
		assert.Equal(t, domain.GateAuto, s.Gate(), s.Stage().String())
		assert.False(t, s.Optional(), s.Stage().String())
	}
	assert.Equal(t, []domain.Stage{domain.StageDiscovery, domain.StageMeasure, domain.StageOptimize, domain.StageReview,
		domain.StageIntegrationTesting, domain.StageStressTesting}, stages)
	assert.True(t, path.StressMandatory())
	assert.False(t, must(domain.PathFor(domain.WorkTypeFixBug)).StressMandatory())

	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageMeasure.WriteScope())
	assert.Equal(t, domain.WriteScopeAnything, domain.StageOptimize.WriteScope())
	assert.Equal(t, []domain.ExitCheck{domain.CheckArtifactWritten, domain.CheckStressScriptsValid, domain.CheckBaselineRecorded, domain.CheckDiagnosisWritten},
		domain.RequiredChecks(domain.StageMeasure, path))
	assert.Equal(t, []domain.ExitCheck{domain.CheckArtifactWritten, domain.CheckBuildPasses, domain.CheckFormatted, domain.CheckTestsUntouched, domain.CheckSuitePasses},
		domain.RequiredChecks(domain.StageOptimize, path))
	assert.False(t, domain.StageMeasure.ChecksCode(), "measure's failures are its own work")
}

func targeting(t *testing.T, ids ...string) []domain.WorkEvent {
	t.Helper()
	var targets []domain.NFRID
	for _, id := range ids {
		targets = append(targets, must(domain.NewNFRID(id)))
	}
	events, err := domain.StartWorkTargeting(workID, domain.WorkTypeImprovePerformance, description, mainBranch, activeOn(t, mainBranch), targets, repoNFRs)
	require.NoError(t, err)
	return events
}

func TestStartWork_ImprovePerformance_TargetsNFRs(t *testing.T) {
	events := targeting(t, "wallet-transaction-latency")
	assert.Equal(t, []domain.NFRID{latency.ID()}, events[0].(domain.WorkStarted).TargetNFRs)
	w := asInProgress(t, givenWork(t, events...))
	assert.Equal(t, []domain.NFRID{latency.ID()}, w.TargetNFRs())
	assert.Equal(t, domain.StageDiscovery, w.CurrentStage())

	_, err := domain.StartWork(workID, domain.WorkTypeImprovePerformance, description, mainBranch, activeOn(t, mainBranch))
	assert.ErrorIs(t, err, domain.ErrTargetNFRsRequired)

	_, err = domain.StartWorkTargeting(workID, domain.WorkTypeImprovePerformance, description, mainBranch, activeOn(t, mainBranch),
		[]domain.NFRID{must(domain.NewNFRID("unknown"))}, repoNFRs)
	var unknown *domain.UnknownTargetNFRError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "wallet-transaction-latency, ledger-write", unknown.KnownIDs())

	_, err = domain.StartWorkTargeting(workID, domain.WorkTypeImprovePerformance, description, mainBranch, activeOn(t, mainBranch),
		[]domain.NFRID{latency.ID(), latency.ID()}, repoNFRs)
	assert.ErrorIs(t, err, domain.ErrInvalidNFR)

	for _, other := range []domain.WorkType{domain.WorkTypeFixBug, domain.WorkTypeNewFeature, domain.WorkTypeRefactor} {
		_, err = domain.StartWorkTargeting(workID, other, description, mainBranch, activeOn(t, mainBranch), []domain.NFRID{latency.ID()}, repoNFRs)
		assert.ErrorIs(t, err, domain.ErrTargetNFRsNotAccepted, other.String())
	}

	started := events[0].(domain.WorkStarted)
	started.TargetNFRs = nil
	_, err = domain.RebuildWork([]domain.WorkEvent{started, events[1]})
	assert.ErrorIs(t, err, domain.ErrInconsistentHistory, "a recorded improve-performance start always has targets")
}

// onMeasure is improve-performance work that targets the latency NFR, on
// its first measure visit.
func onMeasure(t *testing.T) []domain.WorkEvent {
	t.Helper()
	history := targeting(t, "wallet-transaction-latency")
	return advance(t, history)
}

func TestRecordBaseline(t *testing.T) {
	history := onMeasure(t)
	w := asInProgress(t, givenWork(t, history...))
	require.Equal(t, domain.StageMeasure, w.CurrentStage())

	_, err := w.RecordBaseline(nil)
	assert.ErrorIs(t, err, domain.ErrBaselineIncomplete)

	events, err := w.RecordBaseline([]domain.NFRResult{latency.Evaluate(slow), ledgerWrite.Evaluate(slow)})
	require.NoError(t, err)
	assert.Equal(t, []domain.WorkEvent{domain.BaselineMeasured{WorkID: workID, NFR: latency.ID(), Measurement: slow}}, events,
		"only the targets; the target misses, so there is something to improve")
	after := asInProgress(t, givenWork(t, append(history, events...)...))
	assert.Equal(t, []domain.NFRMeasurement{{NFR: latency.ID(), Measurement: slow}}, after.Baseline())

	_, err = asInProgress(t, givenWork(t, targeting(t, "wallet-transaction-latency")...)).RecordBaseline(nil)
	assert.ErrorIs(t, err, domain.ErrNotMeasuring)
}

// A baseline that already meets every target completes the work.
func TestRecordBaseline_NothingToImprove_CompletesTheWork(t *testing.T) {
	history := onMeasure(t)
	events, err := asInProgress(t, givenWork(t, history...)).RecordBaseline([]domain.NFRResult{latency.Evaluate(fast)})
	require.NoError(t, err)
	measurements := []domain.NFRMeasurement{{NFR: latency.ID(), Measurement: fast}}
	assert.Equal(t, []domain.WorkEvent{
		domain.BaselineMeasured{WorkID: workID, NFR: latency.ID(), Measurement: fast},
		domain.WorkFoundNothingToImprove{WorkID: workID, Measurements: measurements},
	}, events)
	done := stateIs[domain.CompletedWork](t, givenWork(t, append(history, events...)...))
	got, ok := done.FoundNothingToImprove()
	assert.True(t, ok)
	assert.Equal(t, measurements, got)

	_, ok = stateIs[domain.CompletedWork](t, givenWork(t, completed()...)).FoundNothingToImprove()
	assert.False(t, ok)
}

// Once the work entered optimize, the code changed: measuring again
// records no new baseline, and never finds nothing to improve.
func TestRecordBaseline_AfterOptimize_RecordsNothing(t *testing.T) {
	history := onMeasure(t)
	w := asInProgress(t, givenWork(t, history...))
	baseline, err := w.RecordBaseline([]domain.NFRResult{latency.Evaluate(slow)})
	require.NoError(t, err)
	history = advance(t, append(history, baseline...)) // optimize
	optimize := asInProgress(t, givenWork(t, history...))
	require.Equal(t, domain.StageOptimize, optimize.CurrentStage())
	history = advance(t, history) // review
	review := asInProgress(t, givenWork(t, history...))
	events, err := review.AdvancePastStage(domain.StageReview, review.CurrentVisit(), domain.MandatoryStress(repoNFRs, changedFiles(), optimize.TargetNFRs()))
	require.NoError(t, err)
	history = append(history, events...) // integration testing
	history = advance(t, history)        // stress testing
	stress := asInProgress(t, givenWork(t, history...))
	events, err = stress.EnterFixer(domain.StageStressTesting, stress.CurrentVisit(), reasonOf("p95 misses"))
	require.NoError(t, err)
	assert.Equal(t, domain.FixerModePerformance, events[0].(domain.FixerEntered).Mode)
	fixer := stateIs[domain.InFixerWork](t, givenWork(t, append(history, events...)...))
	assert.Equal(t, []domain.Stage{domain.StageMeasure, domain.StageOptimize}, fixer.RouteTargets())
	back, err := fixer.ReturnToStage(domain.StageMeasure, reasonOf("wrong diagnosis"))
	require.NoError(t, err)
	history = append(append(history, events...), back...)

	again, err := asInProgress(t, givenWork(t, history...)).RecordBaseline([]domain.NFRResult{latency.Evaluate(fast)})
	require.NoError(t, err)
	assert.Empty(t, again)
	_, err = domain.RebuildWork(append(history, domain.WorkFoundNothingToImprove{WorkID: workID, Measurements: []domain.NFRMeasurement{{NFR: latency.ID(), Measurement: fast}}}))
	assert.ErrorIs(t, err, domain.ErrInconsistentHistory)
}

// Review makes no stress decision on improve-performance work: gf records
// include, forced by the targets and the NFRs covering the change.
func TestMandatoryStress(t *testing.T) {
	choice := domain.MandatoryStress(repoNFRs, changedFiles("internal/ledger/post.go"), []domain.NFRID{latency.ID()})
	assert.Equal(t, domain.StressInclude, choice.Decision())
	assert.Equal(t, []domain.NFRID{latency.ID(), ledgerWrite.ID()}, choice.ForcedBy())

	assert.Equal(t, []domain.NFR{ledgerWrite, latency}, domain.MandatoryNFRs(repoNFRs, changedFiles("internal/ledger/x.go"), []domain.NFRID{ledgerWrite.ID()}),
		"the targets first, then those covering the change")
	assert.Empty(t, domain.MandatoryNFRs(repoNFRs, changedFiles("docs/a.md"), nil))

	history := onMeasure(t)
	w := asInProgress(t, givenWork(t, history...))
	_, err := w.OverrideStressDecision(owner, skipStress)
	assert.ErrorIs(t, err, domain.ErrStressMandatoryOnPath)
}

func TestCompareToBaseline(t *testing.T) {
	both := walletNFR(domain.TargetValues{P95: ptr(ms(150)), MinRPS: ptr(250.0)})
	assert.Equal(t, "p95 212ms → 120ms (target 150ms), rps 240 → 260 (target 250)", domain.CompareToBaseline(both, slow, fast))
}
