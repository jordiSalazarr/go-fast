package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

var (
	skipStress    = must(domain.NewStressChoice(domain.StressSkip, reasonOf("only error messages changed"), nil))
	includeStress = must(domain.NewStressChoice(domain.StressInclude, reasonOf("the change adds a query per request"), nil))
	walletID      = must(domain.NewNFRID("wallet-transaction-latency"))
	forcedInclude = must(domain.NewStressChoice(domain.StressInclude, reasonOf("touches the ledger"), []domain.NFRID{walletID}))
)

// decided is review's stress decision; byOwner is the owner's.
func decided(c domain.StressChoice) domain.WorkEvent {
	return domain.StressDecided{WorkID: workID, Decision: c.Decision(), Reason: c.Reason(), ForcedBy: c.ForcedBy()}
}

func byOwner(c domain.StressChoice) domain.WorkEvent {
	return domain.StressDecided{WorkID: workID, Decision: c.Decision(), Reason: c.Reason(), ForcedBy: c.ForcedBy(), Owner: owner}
}

// stressFor is what advancing past a stage needs: review's choice (skip),
// nothing anywhere else.
func stressFor(stage domain.Stage) domain.StressChoice {
	if stage == domain.StressDecider() {
		return skipStress
	}
	return domain.StressChoice{}
}

// onIntegration is a work past its first review with this decision, on
// integration testing.
func onIntegration(c domain.StressChoice) []domain.WorkEvent {
	return append(onReview(), decided(c), entered(domain.StageIntegrationTesting, 1))
}

func TestStressTesting_IsAnOptionalStageAfterIntegrationTesting_DecidedByReview(t *testing.T) {
	steps := must(domain.PathFor(domain.WorkTypeFixBug)).Steps()
	last := steps[len(steps)-1]
	assert.Equal(t, domain.StageStressTesting, last.Stage())
	assert.True(t, last.Optional())
	assert.Equal(t, domain.GateAuto, last.Gate())
	assert.Equal(t, 3, last.Budget().Int())
	assert.Equal(t, domain.StageIntegrationTesting, steps[len(steps)-2].Stage())
	assert.Equal(t, domain.WriteScopeArtifacts, domain.StageStressTesting.WriteScope())
	for _, s := range steps[:len(steps)-1] {
		assert.False(t, s.Optional(), s.Stage().String())
	}
}

// Review's choice travels on its own submission, so its gate must be auto:
// an approval carries no choice.
func TestTheStressDecider_IsOnEveryPathBeforeItsOptionalStages_WithAnAutoGate(t *testing.T) {
	for _, workType := range domain.WorkTypes() {
		decider := -1
		for i, s := range must(domain.PathFor(workType)).Steps() {
			if s.Stage() == domain.StressDecider() {
				decider = i
				assert.Equal(t, domain.GateAuto, s.Gate(), workType.String())
			}
			if s.Optional() {
				assert.True(t, decider >= 0 && decider < i, "%s: %s is optional but comes before the stress decider", workType, s.Stage())
			}
		}
	}
}

// Given the repository's NFRs and the files the work changed, when a stress
// decision is chosen, then skip is impossible if an NFR covers the change.
func TestChooseStress(t *testing.T) {
	wallet := walletNFR(domain.TargetValues{P95: ptr(ms(150))})
	nfrs := []domain.NFR{wallet}

	t.Run("no NFR covers the change: skip", func(t *testing.T) {
		c, err := domain.ChooseStress(domain.StressSkip, reasonOf("docs only"), nfrs, changedFiles("docs/a.md"))
		require.NoError(t, err)
		assert.Equal(t, domain.StressSkip, c.Decision())
		assert.Empty(t, c.ForcedBy())
	})
	t.Run("no NFR covers the change: include", func(t *testing.T) {
		c, err := domain.ChooseStress(domain.StressInclude, reasonOf("new query"), nfrs, changedFiles("internal/users/find.go"))
		require.NoError(t, err)
		assert.Equal(t, domain.StressInclude, c.Decision())
		assert.Empty(t, c.ForcedBy())
	})
	t.Run("an NFR covers the change: skip is rejected naming it and its areas", func(t *testing.T) {
		_, err := domain.ChooseStress(domain.StressSkip, reasonOf("small change"), nfrs, changedFiles("internal/ledger/entry.go"))
		require.ErrorIs(t, err, domain.ErrStressForced)
		var forced *domain.StressForcedError
		require.ErrorAs(t, err, &forced)
		assert.Equal(t, []domain.NFR{wallet}, forced.NFRs)
		assert.Equal(t, "wallet-transaction-latency (internal/wallet/**, internal/ledger/**)", forced.Describe())
	})
	t.Run("an NFR covers the change: include is forced by it", func(t *testing.T) {
		c, err := domain.ChooseStress(domain.StressInclude, reasonOf("touches the ledger"), nfrs, changedFiles("internal/ledger/entry.go"))
		require.NoError(t, err)
		assert.Equal(t, []domain.NFRID{walletID}, c.ForcedBy())
	})
	t.Run("no NFRs at all: never mandatory", func(t *testing.T) {
		_, err := domain.ChooseStress(domain.StressSkip, reasonOf("x"), nil, changedFiles("internal/ledger/entry.go"))
		require.NoError(t, err)
	})
	t.Run("a decision needs a reason", func(t *testing.T) {
		_, err := domain.ChooseStress(domain.StressInclude, domain.Reason{}, nfrs, changedFiles("a.go"))
		require.ErrorIs(t, err, domain.ErrMissingValue)
	})
}

func TestNewStressChoice_ASkipCantHaveBeenForced(t *testing.T) {
	_, err := domain.NewStressChoice(domain.StressSkip, reasonOf("x"), []domain.NFRID{walletID})
	require.ErrorIs(t, err, domain.ErrStressForced)
	_, err = domain.ParseStressDecision("maybe")
	require.ErrorIs(t, err, domain.ErrUnknownStressDecision)
}

func TestAdvancePastReview_WithoutAStressDecision_IsRejected(t *testing.T) {
	w := asInProgress(t, givenWork(t, onReview()...))

	events, err := w.AdvancePastStage(domain.StageReview, visit1, domain.StressChoice{})

	thenRejected(t, events, err, domain.ErrMissingStressDecision)
}

func TestAdvancePastAnotherStage_WithAStressDecision_IsRejected(t *testing.T) {
	w := asInProgress(t, givenWork(t, onIntegration(skipStress)...))

	events, err := w.AdvancePastStage(domain.StageIntegrationTesting, visit1, includeStress)

	thenRejected(t, events, err, domain.ErrStressOutsideReview)
}

// Given review's decision, when the work advances, then stress testing is
// entered after integration testing only if review included it.
func TestAdvancePastReview_RecordsTheDecision_AndStressTestingRunsOnlyIfIncluded(t *testing.T) {
	t.Run("skip: integration testing is the last stage", func(t *testing.T) {
		w := asInProgress(t, givenWork(t, onReview()...))
		events, err := w.AdvancePastStage(domain.StageReview, visit1, skipStress)
		thenEvents(t, events, err, decided(skipStress), entered(domain.StageIntegrationTesting, 1))
		choice, decider, ok := asInProgress(t, thenWorkState(t, onReview(), events)).StressDecision()
		require.True(t, ok)
		assert.Equal(t, skipStress, choice)
		assert.Equal(t, domain.Owner{}, decider, "review decided")

		w = asInProgress(t, givenWork(t, onIntegration(skipStress)...))
		events, err = w.AdvancePastStage(domain.StageIntegrationTesting, visit1, domain.StressChoice{})
		thenEvents(t, events, err, domain.WorkEvent(domain.WorkCompleted{WorkID: workID}))
	})
	t.Run("include: stress testing follows integration testing", func(t *testing.T) {
		w := asInProgress(t, givenWork(t, onReview()...))
		events, err := w.AdvancePastStage(domain.StageReview, visit1, forcedInclude)
		thenEvents(t, events, err, decided(forcedInclude), entered(domain.StageIntegrationTesting, 1))

		given := onIntegration(forcedInclude)
		w = asInProgress(t, givenWork(t, given...))
		events, err = w.AdvancePastStage(domain.StageIntegrationTesting, visit1, domain.StressChoice{})
		thenEvents(t, events, err, entered(domain.StageStressTesting, 1))

		given = append(given, events...)
		w = asInProgress(t, givenWork(t, given...))
		assert.Equal(t, domain.StageStressTesting, w.CurrentStage())
		events, err = w.AdvancePastStage(domain.StageStressTesting, visit1, domain.StressChoice{})
		thenEvents(t, events, err, domain.WorkEvent(domain.WorkCompleted{WorkID: workID}))
	})
}

// Given the owner decided before review, when review passes with its own
// choice, then the owner's decision stands, unless the change now forces
// what the owner skipped.
func TestAdvancePastReview_AfterTheOwnerDecided(t *testing.T) {
	ownerOnImplement := func(c domain.StressChoice) []domain.WorkEvent {
		h := append(started(), entered(domain.StageSpecify, 1), entered(domain.StageImplement, 1), byOwner(c))
		return append(h, entered(domain.StageReview, 1))
	}
	t.Run("the owner's include stands over review's skip", func(t *testing.T) {
		w := asInProgress(t, givenWork(t, ownerOnImplement(includeStress)...))
		events, err := w.AdvancePastStage(domain.StageReview, visit1, skipStress)
		thenEvents(t, events, err, entered(domain.StageIntegrationTesting, 1))
		choice, decider, _ := asInProgress(t, thenWorkState(t, ownerOnImplement(includeStress), events)).StressDecision()
		assert.Equal(t, includeStress, choice)
		assert.Equal(t, owner, decider)
	})
	t.Run("the owner's skip stands over review's unforced include", func(t *testing.T) {
		w := asInProgress(t, givenWork(t, ownerOnImplement(skipStress)...))
		events, err := w.AdvancePastStage(domain.StageReview, visit1, includeStress)
		thenEvents(t, events, err, entered(domain.StageIntegrationTesting, 1))
	})
	t.Run("a change that now forces stress testing overrides the owner's skip", func(t *testing.T) {
		w := asInProgress(t, givenWork(t, ownerOnImplement(skipStress)...))
		events, err := w.AdvancePastStage(domain.StageReview, visit1, forcedInclude)
		thenEvents(t, events, err, decided(forcedInclude), entered(domain.StageIntegrationTesting, 1))
	})
}

// Given work that hasn't passed integration testing, when the owner
// overrides the stress decision, then it is recorded as theirs.
func TestOverrideStressDecision(t *testing.T) {
	cases := map[string][]domain.WorkEvent{
		"on implement, before review decided": append(started(), entered(domain.StageSpecify, 1), entered(domain.StageImplement, 1)),
		"on review":                           onReview(),
		"on integration testing":              onIntegration(skipStress),
	}
	for name, given := range cases {
		t.Run(name, func(t *testing.T) {
			w := asInProgress(t, givenWork(t, given...))

			events, err := w.OverrideStressDecision(owner, includeStress)

			thenEvents(t, events, err, byOwner(includeStress))
			choice, decider, ok := asInProgress(t, thenWorkState(t, given, events)).StressDecision()
			require.True(t, ok)
			assert.Equal(t, includeStress, choice)
			assert.Equal(t, owner, decider)
		})
	}
}

func TestOverrideStressDecision_OnIntegrationTesting_DecidesWhetherStressTestingRuns(t *testing.T) {
	given := append(onIntegration(skipStress), byOwner(includeStress))
	w := asInProgress(t, givenWork(t, given...))

	events, err := w.AdvancePastStage(domain.StageIntegrationTesting, visit1, domain.StressChoice{})

	thenEvents(t, events, err, entered(domain.StageStressTesting, 1))

	given = append(onIntegration(forcedInclude), byOwner(skipStress))
	w = asInProgress(t, givenWork(t, given...))
	events, err = w.AdvancePastStage(domain.StageIntegrationTesting, visit1, domain.StressChoice{})
	thenEvents(t, events, err, domain.WorkEvent(domain.WorkCompleted{WorkID: workID}))
}

func TestOverrideStressDecision_AfterIntegrationTesting_IsRejected(t *testing.T) {
	given := append(onIntegration(includeStress), entered(domain.StageStressTesting, 1))
	w := asInProgress(t, givenWork(t, given...))

	events, err := w.OverrideStressDecision(owner, skipStress)

	thenRejected(t, events, err, domain.ErrStressDecisionClosed)
}

func TestOverrideStressDecision_NeedsTheOwnerAndAChoice(t *testing.T) {
	w := asInProgress(t, givenWork(t, onReview()...))

	events, err := w.OverrideStressDecision(domain.Owner{}, skipStress)
	thenRejected(t, events, err, domain.ErrMissingValue)
	events, err = w.OverrideStressDecision(owner, domain.StressChoice{})
	thenRejected(t, events, err, domain.ErrMissingValue)
}

// Given a rework, when the work reaches review again, then review decides
// stress testing again: the diff may have changed.
func TestRework_ClearsTheStressDecision_SoTheNextReviewDecidesAgain(t *testing.T) {
	given := join(onIntegration(includeStress), intoFixer(domain.StageIntegrationTesting, 1, 1), backTo(domain.StageImplement, 2))
	w := asInProgress(t, givenWork(t, given...))
	_, _, ok := w.StressDecision()
	assert.False(t, ok, "cleared by the rework")

	given = append(given, entered(domain.StageReview, 2))
	w = asInProgress(t, givenWork(t, given...))
	events, err := w.AdvancePastStage(domain.StageReview, visit(2), domain.StressChoice{})
	thenRejected(t, events, err, domain.ErrMissingStressDecision)

	events, err = w.AdvancePastStage(domain.StageReview, visit(2), skipStress)
	thenEvents(t, events, err, decided(skipStress), entered(domain.StageIntegrationTesting, 2))
	w = asInProgress(t, thenWorkState(t, given, events))
	events, err = w.AdvancePastStage(domain.StageIntegrationTesting, visit(2), domain.StressChoice{})
	thenEvents(t, events, err, domain.WorkEvent(domain.WorkCompleted{WorkID: workID})) // the earlier include no longer applies
}

func TestRebuildWork_RejectsHistoriesThatBreakTheStressDecision(t *testing.T) {
	stressEntered := entered(domain.StageStressTesting, 1)
	forcedSkip := domain.StressDecided{WorkID: workID, Decision: domain.StressSkip, Reason: reasonOf("x"), ForcedBy: []domain.NFRID{walletID}}
	cases := map[string][]domain.WorkEvent{
		"past review without a decision":                append(onReview(), entered(domain.StageIntegrationTesting, 1)),
		"stress testing entered after a skip":           append(onIntegration(skipStress), stressEntered),
		"completed while stress testing was included":   append(onIntegration(includeStress), domain.WorkCompleted{WorkID: workID}),
		"review's decision on another stage":            append(started(), decided(skipStress)),
		"a skip forced by an NFR":                       append(onReview(), forcedSkip),
		"a decision without a reason":                   append(onReview(), domain.StressDecided{WorkID: workID, Decision: domain.StressSkip}),
		"the owner's decision after integration passed": append(onIntegration(includeStress), stressEntered, byOwner(skipStress)),
		"past review again after a rework, with the old decision only": join(onIntegration(includeStress), intoFixer(domain.StageIntegrationTesting, 1, 1),
			backTo(domain.StageImplement, 2), []domain.WorkEvent{entered(domain.StageReview, 2), entered(domain.StageIntegrationTesting, 2)}),
	}
	for name, history := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := domain.RebuildWork(history)
			require.ErrorIs(t, err, domain.ErrInconsistentHistory)
		})
	}
}

// Review's pass must decide stress testing, and only review's may: the
// choice is checked before any exit check runs.
func TestCheckClaim_StressDecision(t *testing.T) {
	openOn := func(stage domain.Stage) domain.Open {
		step := domain.FixerStep()
		for _, s := range must(domain.PathFor(domain.WorkTypeFixBug)).Steps() {
			if s.Stage() == stage {
				step = s
			}
		}
		return stateIs[domain.Open](t, givenAssignment(t, must(domain.OpenAssignment(workID, stage, visit1, step.Gate(), step.Budget()))...))
	}
	review, implement := openOn(domain.StageReview), openOn(domain.StageImplement)

	require.ErrorIs(t, review.CheckClaim(domain.ClaimPassed()), domain.ErrMissingStressDecision)
	require.NoError(t, review.CheckClaim(domain.ClaimStressDecided(skipStress)))
	require.NoError(t, review.CheckClaim(domain.ClaimFailed(reasonOf("blocking finding"))), "a failed claim decides nothing")
	require.ErrorIs(t, implement.CheckClaim(domain.ClaimStressDecided(skipStress)), domain.ErrStressOutsideReview)

	events, err := review.SubmitForAcceptance(domain.WorkTypeFixBug, domain.ClaimPassed(), domain.NewChangedFiles(), passedChecks(review))
	thenRejected(t, events, err, domain.ErrMissingStressDecision)
}

func TestReviewAccepted_CarriesItsStressDecision(t *testing.T) {
	given := must(domain.OpenAssignment(workID, domain.StageReview, visit1, domain.GateAuto, budget3))
	review := stateIs[domain.Open](t, givenAssignment(t, given...))

	events, err := review.SubmitForAcceptance(domain.WorkTypeFixBug, domain.ClaimStressDecided(forcedInclude), domain.NewChangedFiles(), passedChecks(review))

	thenEvents(t, events, err, domain.AssignmentEvent(domain.AssignmentAccepted{
		AssignmentID: review.ID(), WorkID: workID, Stage: domain.StageReview, Visit: visit1, Stress: forcedInclude,
	}))
	stateIs[domain.Accepted](t, thenAssignmentState(t, given, events))
}

func TestRebuildAssignment_RejectsAReviewAcceptedWithoutADecision_OrAnotherStageWithOne(t *testing.T) {
	review := must(domain.OpenAssignment(workID, domain.StageReview, visit1, domain.GateAuto, budget3))
	implement := must(domain.OpenAssignment(workID, domain.StageImplement, visit1, domain.GateAuto, budget3))
	for name, history := range map[string][]domain.AssignmentEvent{
		"review without": append(review, domain.AssignmentAccepted{AssignmentID: review[0].(domain.AssignmentOpened).AssignmentID,
			WorkID: workID, Stage: domain.StageReview, Visit: visit1}),
		"implement with": append(implement, domain.AssignmentAccepted{AssignmentID: implement[0].(domain.AssignmentOpened).AssignmentID,
			WorkID: workID, Stage: domain.StageImplement, Visit: visit1, Stress: skipStress}),
	} {
		_, err := domain.RebuildAssignment(history)
		require.ErrorIs(t, err, domain.ErrInconsistentHistory, name)
	}
}

// Given stress testing found an NFR missed, when the work enters the fixer,
// then the fixer looks for a performance problem, and may send the work
// back to specify or implement.
func TestEnterFixer_FromStressTesting_IsInPerformanceMode(t *testing.T) {
	given := append(onIntegration(includeStress), entered(domain.StageStressTesting, 1))
	w := asInProgress(t, givenWork(t, given...))

	events, err := w.EnterFixer(domain.StageStressTesting, visit1, reasonOf("nfrs-met: wallet: p95 212ms > 150ms."))

	thenEvents(t, events, err,
		domain.WorkEvent(domain.FixerEntered{WorkID: workID, From: domain.StageStressTesting, FromVisit: visit1, Visit: visit1,
			Mode: domain.FixerModePerformance, Reason: reasonOf("nfrs-met: wallet: p95 212ms > 150ms.")}),
		entered(domain.StageFixer, 1))
	fixer := stateIs[domain.InFixerWork](t, thenWorkState(t, given, events))
	assert.Equal(t, domain.FixerModePerformance, fixer.Mode())
	assert.Equal(t, []domain.Stage{domain.StageSpecify, domain.StageImplement}, fixer.RouteTargets())
}

func TestRebuildWork_RejectsAFixerFromStressTestingInCorrectnessMode(t *testing.T) {
	history := append(onIntegration(includeStress), entered(domain.StageStressTesting, 1),
		domain.FixerEntered{WorkID: workID, From: domain.StageStressTesting, FromVisit: visit1, Visit: visit1,
			Mode: domain.FixerModeCorrectness, Reason: reasonOf("slow")},
		entered(domain.StageFixer, 1))

	_, err := domain.RebuildWork(history)

	require.ErrorIs(t, err, domain.ErrInconsistentHistory)
}

func TestStressScripts_LiveInTheWorksArtifactDirectory(t *testing.T) {
	assert.Equal(t, ".gofast/works/w1/stress", domain.StressScriptsDir(workID).String())
	assert.Equal(t, ".gofast/works/w1/stress/wallet-transaction-latency.js", domain.StressScriptFor(workID, walletID).String())
	assert.True(t, domain.StageStressTesting.WriteScope().Allows(domain.StressScriptFor(workID, walletID), workID))
}
