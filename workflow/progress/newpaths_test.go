package progress_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

var latency = Must(domain.NewNFR(Must(domain.NewNFRID("wallet-latency")), "", []domain.AreaGlob{Must(domain.NewAreaGlob("internal/wallet/**"))},
	Must(domain.NewOperation("POST /wallets/{id}/transactions")), Must(domain.NewLoad(2, time.Second)),
	Must(domain.NewTargets(domain.TargetValues{P95: func() *time.Duration { d := 150 * time.Millisecond; return &d }()}))))

var slow = Must(domain.NewMeasurement(domain.MeasurementValues{P95: 212 * time.Millisecond, P99: 300 * time.Millisecond, RPS: 10, Requests: 100}))

func queriedWith(t *testing.T, s *eventlogtest.Scenario, facts progress.Facts) progress.View {
	t.Helper()
	var view progress.View
	s.WhenQueried(func(log *eventlog.Snapshot) error {
		var err error
		view, err = progress.Read(log, Branch, facts)
		return err
	})
	return view
}

// walk enters every stage of a work type's path up to stage, accepting
// each visit before it, and opens stage's assignment.
func walk(workType domain.WorkType, stage domain.Stage, started domain.WorkStarted, between func(domain.Stage) []domain.Event) []domain.Event {
	path := Must(domain.PathFor(workType))
	events := []domain.Event{started}
	for _, step := range path.Steps() {
		entered := domain.StageEntered{WorkID: WorkID, Stage: step.Stage(), Visit: Visit1, Gate: step.Gate(), Budget: step.Budget()}
		events = append(events, entered)
		id := domain.AssignmentIDFor(WorkID, step.Stage(), Visit1)
		events = append(events, domain.AssignmentOpened{AssignmentID: id, WorkID: WorkID, Stage: step.Stage(), Visit: Visit1, Gate: step.Gate(), Budget: step.Budget()})
		if step.Stage() == stage {
			return events
		}
		if step.Gate() == domain.GateHuman {
			events = append(events, domain.SubmittedForApproval{AssignmentID: id, Attempt: Attempt(1)})
		}
		events = append(events, domain.AssignmentAccepted{AssignmentID: id, WorkID: WorkID, Stage: step.Stage(), Visit: Visit1})
		if between != nil {
			events = append(events, between(step.Stage())...)
		}
	}
	return events
}

func TestStatus_OnARefactor_ShowsWhatMustNotChangeThePlanAndTheCoverage(t *testing.T) {
	started := domain.WorkStarted{WorkID: WorkID, WorkType: domain.WorkTypeRefactor, PathVersion: domain.PathV1, Description: Description, Branch: Branch}
	plan := domain.NewRefactorPlan(domain.RefactorPlanValues{Areas: []string{"internal/wallet/**"}})
	facts := progress.Facts{
		Grill: func(domain.RepoPath) (domain.Grill, error) {
			return domain.NewGrill(domain.GrillValues{MustNotChange: []string{"POST /wallets keeps its wire format"}}), nil
		},
		Plan: func(domain.RepoPath) (domain.RefactorPlan, error) { return plan, nil },
		Coverage: func(domain.AssignmentID) (int, exitchecks.CoverageResult, bool, error) {
			return 2, exitchecks.CoverageResult{MinCoverage: 80, Packages: []exitchecks.PackageCoverage{{Package: "m/internal/wallet", Dir: "internal/wallet", Statements: 10, Covered: 9, Percent: 90}}}, true, nil
		},
	}
	v := queriedWith(t, eventlogtest.Given(t, walk(domain.WorkTypeRefactor, domain.StageImplement, started, nil)...), facts)

	require.NotNil(t, v.Work.Grill)
	assert.Equal(t, []string{"POST /wallets keeps its wire format"}, v.Work.Grill.MustNotChange)
	require.NotNil(t, v.Work.Plan)
	assert.Equal(t, []string{"internal/wallet/**"}, v.Work.Plan.Areas)
	assert.True(t, v.Work.Plan.Accepted)
	require.NotNil(t, v.Work.Coverage)
	assert.Equal(t, 90.0, v.Work.Coverage.Packages[0].Percent)
	var text strings.Builder
	progress.RenderText(&text, v)
	for _, want := range []string{"Must not change:\n  - POST /wallets keeps its wire format\n", "Plan (visit 1, accepted): .gofast/works/w1/plan-v1.md; implement may change only: internal/wallet/**\n",
		"Coverage (characterize visit 1, attempt 2; minimum 80%): internal/wallet 90% (9 of 10 statements)\n"} {
		assert.Contains(t, text.String(), want)
	}
}

func TestStatus_OnImprovePerformance_ShowsTheTargetsTheBaselineAndTheSkillsLoaded(t *testing.T) {
	started := domain.WorkStarted{WorkID: WorkID, WorkType: domain.WorkTypeImprovePerformance, PathVersion: domain.PathV1, Description: Description, Branch: Branch,
		TargetNFRs: []domain.NFRID{latency.ID()}}
	baseline := func(stage domain.Stage) []domain.Event {
		if stage != domain.StageMeasure {
			return nil
		}
		return []domain.Event{domain.BaselineMeasured{WorkID: WorkID, NFR: latency.ID(), Measurement: slow,
			Script: domain.StressScriptFor(WorkID, latency.ID()), ScriptHash: domain.HashOf([]byte("export default function () {}"))}}
	}
	events := append(walk(domain.WorkTypeImprovePerformance, domain.StageOptimize, started, baseline),
		domain.SkillLoaded{WorkID: WorkID, Stage: domain.StageOptimize, Visit: Visit1, Skill: "concurrency"},
		domain.SkillLoaded{WorkID: WorkID, Stage: domain.StageMeasure, Visit: Visit1, Skill: "memory"})
	v := queriedWith(t, eventlogtest.Given(t, events...), progress.Facts{NFRs: func() ([]domain.NFR, error) { return []domain.NFR{latency}, nil }})

	assert.Equal(t, []string{"wallet-latency"}, v.Work.TargetNFRs)
	hash := domain.HashOf([]byte("export default function () {}")).String()[:12]
	assert.Equal(t, []progress.BaselineView{{NFR: "wallet-latency", Measured: slow.Describe(), Targets: "p95 ≤ 150ms", Met: false,
		Script: ".gofast/works/w1/stress/wallet-latency.js", ScriptHash: hash}}, v.Work.Baseline)
	assert.Equal(t, []string{"concurrency"}, v.Current.SkillsLoaded, "only this visit's")
	var text strings.Builder
	progress.RenderText(&text, v)
	assert.Contains(t, text.String(), "Target NFRs: wallet-latency\n")
	assert.Contains(t, text.String(), "Baseline of wallet-latency: p95 212ms, ")
	assert.Contains(t, text.String(), "  script .gofast/works/w1/stress/wallet-latency.js: frozen (sha256 "+hash+")\n")
	assert.Contains(t, text.String(), "Skills loaded this visit: concurrency\n")
}

func TestStatus_AfterNothingToImprove_SaysWhy(t *testing.T) {
	started := domain.WorkStarted{WorkID: WorkID, WorkType: domain.WorkTypeImprovePerformance, PathVersion: domain.PathV1, Description: Description, Branch: Branch,
		TargetNFRs: []domain.NFRID{latency.ID()}}
	fast := Must(domain.NewMeasurement(domain.MeasurementValues{P95: 100 * time.Millisecond, Requests: 100}))
	events := append(walk(domain.WorkTypeImprovePerformance, domain.StageMeasure, started, nil),
		domain.BaselineMeasured{WorkID: WorkID, NFR: latency.ID(), Measurement: fast},
		domain.WorkFoundNothingToImprove{WorkID: WorkID, Measurements: []domain.NFRMeasurement{{NFR: latency.ID(), Measurement: fast}}})
	v := queriedWith(t, eventlogtest.Given(t, events...), progress.Facts{})

	assert.Nil(t, v.Work)
	assert.Contains(t, v.Next.Message, "The last work on branch main found nothing to improve: every target NFR already met its targets at baseline (wallet-latency: p95 100ms")
	assert.Contains(t, v.Next.Message, "tighten the NFR with `gf nfrs adopt` on a later design, or start new work with a stricter NFR")
}

func TestStatus_OnPerformanceReview_NoStressDecisionToMake(t *testing.T) {
	started := domain.WorkStarted{WorkID: WorkID, WorkType: domain.WorkTypeImprovePerformance, PathVersion: domain.PathV1, Description: Description, Branch: Branch,
		TargetNFRs: []domain.NFRID{latency.ID()}}
	baseline := func(stage domain.Stage) []domain.Event {
		if stage != domain.StageMeasure {
			return nil
		}
		return []domain.Event{domain.BaselineMeasured{WorkID: WorkID, NFR: latency.ID(), Measurement: slow}}
	}
	v := queriedWith(t, eventlogtest.Given(t, walk(domain.WorkTypeImprovePerformance, domain.StageReview, started, baseline)...),
		progress.Facts{Stress: func(domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error) {
			return []domain.NFR{latency}, domain.NewChangedFiles(), nil
		}})
	assert.Equal(t, []string{"gf submit --passed", `gf submit --failed "<reason>"`}, v.Next.Commands)
	require.NotNil(t, v.Current.Stress)
	require.Len(t, v.Current.Stress.Mandatory, 1, "the target is mandatory though the change doesn't touch it")
	assert.Equal(t, "wallet-latency", v.Current.Stress.Mandatory[0].ID)
}
