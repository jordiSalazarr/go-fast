package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func visit(n int) domain.Visit { return must(domain.NewVisit(n)) }

// entered is the StageEntered of a stage visit, with the stage's gate and budget.
func entered(stage domain.Stage, v int) domain.WorkEvent {
	step := domain.FixerStep()
	for _, s := range must(domain.PathFor(domain.WorkTypeFixBug)).Steps() {
		if s.Stage() == stage {
			step = s
		}
	}
	return domain.StageEntered{WorkID: workID, Stage: stage, Visit: visit(v), Gate: step.Gate(), Budget: step.Budget()}
}

// onReview is the history of a work that reached its first review.
func onReview() []domain.WorkEvent {
	return append(started(), entered(domain.StageSpecify, 1), entered(domain.StageImplement, 1), entered(domain.StageReview, 1))
}

// intoFixer is a handover from a stage visit entering the fixer's nth visit.
func intoFixer(from domain.Stage, fromVisit, n int) []domain.WorkEvent {
	return []domain.WorkEvent{
		domain.FixerEntered{WorkID: workID, From: from, FromVisit: visit(fromVisit), Visit: visit(n), Mode: domain.FixerModeCorrectness, Reason: reasonOf("suite fails")},
		entered(domain.StageFixer, n),
	}
}

// backTo is the fixer returning the work to a new visit of target.
func backTo(target domain.Stage, v int) []domain.WorkEvent {
	return []domain.WorkEvent{
		domain.ReworkStarted{WorkID: workID, Target: target, Visit: visit(v), Reason: reasonOf("the fix misses a case")},
		entered(target, v),
	}
}

func join(parts ...[]domain.WorkEvent) []domain.WorkEvent {
	var all []domain.WorkEvent
	for _, p := range parts {
		all = append(all, p...)
	}
	return all
}

// reworkedTimes is a work that went from review to the fixer and back to
// implement n times, and is on review again.
func reworkedTimes(n int) []domain.WorkEvent {
	h := onReview()
	for i := 1; i <= n; i++ {
		h = join(h, intoFixer(domain.StageReview, i, i), backTo(domain.StageImplement, i+1), []domain.WorkEvent{entered(domain.StageReview, i+1)})
	}
	return h
}

func TestEnterFixer_FromAStageThatChecksCode_EntersTheFixer(t *testing.T) {
	for _, from := range []domain.Stage{domain.StageReview, domain.StageIntegrationTesting} {
		t.Run(from.String(), func(t *testing.T) {
			given := onReview()
			if from == domain.StageIntegrationTesting {
				given = append(given, entered(domain.StageIntegrationTesting, 1))
			}
			w := asInProgress(t, givenWork(t, given...))

			events, err := w.EnterFixer(from, visit1, reasonOf("suite fails"))

			thenEvents(t, events, err, intoFixer(from, 1, 1)...)
			fixer := stateIs[domain.InFixerWork](t, thenWorkState(t, given, events))
			assert.Equal(t, domain.StageFixer, fixer.CurrentStage())
			assert.Equal(t, visit1, fixer.CurrentVisit())
			assert.Equal(t, from, fixer.From())
			assert.Equal(t, domain.FixerModeCorrectness, fixer.Mode())
			assert.Equal(t, 1, fixer.Reworks().Int())
			assert.Equal(t, domain.AssignmentIDFor(workID, domain.StageFixer, visit1), fixer.CurrentAssignment())
			assert.Equal(t, domain.FixerStep(), fixer.CurrentStep())
		})
	}
}

func TestEnterFixer_FromAStageVisitThatIsNotCurrent_IsRejected(t *testing.T) {
	w := asInProgress(t, givenWork(t, onReview()...))

	for _, c := range []struct {
		stage domain.Stage
		visit domain.Visit
	}{{domain.StageIntegrationTesting, visit1}, {domain.StageReview, visit(2)}} {
		events, err := w.EnterFixer(c.stage, c.visit, reason)

		thenRejected(t, events, err, domain.ErrNotCurrentStage)
	}
}

func TestEnterFixer_FromAStageThatCanChangeCode_IsRejected(t *testing.T) {
	given := append(started(), entered(domain.StageSpecify, 1), entered(domain.StageImplement, 1))
	w := asInProgress(t, givenWork(t, given...))

	events, err := w.EnterFixer(domain.StageImplement, visit1, reason)

	thenRejected(t, events, err, domain.ErrCannotEnterFixer)
}

func TestReturnToStage_StartsANewVisitAndEveryLaterStageRunsAgain(t *testing.T) {
	given := join(onReview(), intoFixer(domain.StageReview, 1, 1))
	fixer := stateIs[domain.InFixerWork](t, givenWork(t, given...))
	assert.Equal(t, []domain.Stage{domain.StageSpecify, domain.StageImplement}, fixer.RouteTargets())

	events, err := fixer.ReturnToStage(domain.StageImplement, reasonOf("the fix misses a case"))

	thenEvents(t, events, err, backTo(domain.StageImplement, 2)...)
	history := append(given, events...)
	w := asInProgress(t, givenWork(t, history...))
	assert.Equal(t, domain.StageImplement, w.CurrentStage())
	assert.Equal(t, visit(2), w.CurrentVisit())

	// The path continues in order: review again, then integration testing
	// for the first time, then completion.
	for _, next := range []domain.WorkEvent{entered(domain.StageReview, 2), entered(domain.StageIntegrationTesting, 1)} {
		w = asInProgress(t, givenWork(t, history...))
		events, err = w.AdvancePastStage(w.CurrentStage(), w.CurrentVisit())
		thenEvents(t, events, err, next)
		history = append(history, events...)
	}
	w = asInProgress(t, givenWork(t, history...))
	events, err = w.AdvancePastStage(domain.StageIntegrationTesting, visit1)
	thenEvents(t, events, err, domain.WorkEvent(domain.WorkCompleted{WorkID: workID}))
}

func TestReturnToStage_Specify_StartsItsSecondVisitOnItsHumanGate(t *testing.T) {
	given := join(onReview(), intoFixer(domain.StageReview, 1, 1))
	fixer := stateIs[domain.InFixerWork](t, givenWork(t, given...))

	events, err := fixer.ReturnToStage(domain.StageSpecify, reasonOf("the spec misses negative ports"))

	thenEvents(t, events, err,
		domain.WorkEvent(domain.ReworkStarted{WorkID: workID, Target: domain.StageSpecify, Visit: visit(2), Reason: reasonOf("the spec misses negative ports")}),
		domain.WorkEvent(domain.StageEntered{WorkID: workID, Stage: domain.StageSpecify, Visit: visit(2), Gate: domain.GateHuman, Budget: budget3}))
	w := asInProgress(t, thenWorkState(t, given, events))
	next, err := w.AdvancePastStage(domain.StageSpecify, visit(2))
	thenEvents(t, next, err, entered(domain.StageImplement, 2))
}

func TestReturnToStage_ToAStageThatIsNotAnEarlierCodeStageOnThePath_IsRejectedNamingTheAllowedOnes(t *testing.T) {
	fixer := stateIs[domain.InFixerWork](t, givenWork(t, join(onReview(), intoFixer(domain.StageReview, 1, 1))...))

	for _, target := range []domain.Stage{
		domain.StageReview,             // the stage that handed over
		domain.StageIntegrationTesting, // later
		domain.StageDiscovery,          // can't change code
		domain.StageFixer,              // not on the path
	} {
		t.Run(target.String(), func(t *testing.T) {
			events, err := fixer.ReturnToStage(target, reason)

			thenRejected(t, events, err, domain.ErrInvalidRoute)
			var route *domain.InvalidRouteError
			require.ErrorAs(t, err, &route)
			assert.Equal(t, []domain.Stage{domain.StageSpecify, domain.StageImplement}, route.Allowed)
			assert.Contains(t, err.Error(), "is not one of specify, implement")
		})
	}
}

func TestReturnToStage_WithoutAReason_IsRejected(t *testing.T) {
	fixer := stateIs[domain.InFixerWork](t, givenWork(t, join(onReview(), intoFixer(domain.StageReview, 1, 1))...))

	events, err := fixer.ReturnToStage(domain.StageImplement, domain.Reason{})

	thenRejected(t, events, err, domain.ErrMissingValue)
}

func TestEnterFixer_AfterTheWholeReworkBudget_ReachesTheReworkLimit(t *testing.T) {
	given := reworkedTimes(3)
	w := asInProgress(t, givenWork(t, given...))
	assert.Equal(t, 3, w.Reworks().Int())
	assert.Equal(t, domain.DefaultReworkBudget(), w.ReworkBudget())

	events, err := w.EnterFixer(domain.StageReview, visit(4), reason)

	thenEvents(t, events, err, domain.WorkEvent(domain.ReworkLimitReached{WorkID: workID, Reworks: must(domain.NewReworkCount(3))}))
	limit := stateIs[domain.ReworkLimitReachedWork](t, thenWorkState(t, given, events))
	assert.Equal(t, domain.StageReview, limit.CurrentStage(), "still on the stage that handed over")
	assert.Equal(t, visit(4), limit.CurrentVisit())
}

func TestAllowMoreRework_PutsTheWorkBackInProgressSoTheHandoverEntersTheFixer(t *testing.T) {
	given := append(reworkedTimes(3), domain.ReworkLimitReached{WorkID: workID, Reworks: must(domain.NewReworkCount(3))})
	limit := stateIs[domain.ReworkLimitReachedWork](t, givenWork(t, given...))

	events, err := limit.AllowMoreRework(owner, must(domain.NewReworkBudget(1)))

	thenEvents(t, events, err, domain.WorkEvent(domain.ReworkBudgetExtended{
		WorkID: workID, Owner: owner, Additional: must(domain.NewReworkBudget(1)), NewBudget: must(domain.NewReworkBudget(4)),
	}))
	given = append(given, events...)
	w := asInProgress(t, givenWork(t, given...))
	assert.Equal(t, 4, w.ReworkBudget().Int())

	events, err = w.EnterFixer(domain.StageReview, visit(4), reasonOf("suite fails"))

	thenEvents(t, events, err, intoFixer(domain.StageReview, 4, 4)...)
}

func TestAllowMoreRework_WithoutOwnerOrBudget_IsRejected(t *testing.T) {
	limit := stateIs[domain.ReworkLimitReachedWork](t, givenWork(t,
		append(reworkedTimes(3), domain.ReworkLimitReached{WorkID: workID, Reworks: must(domain.NewReworkCount(3))})...))

	events, err := limit.AllowMoreRework(domain.Owner{}, must(domain.NewReworkBudget(1)))
	thenRejected(t, events, err, domain.ErrMissingValue)
	events, err = limit.AllowMoreRework(owner, domain.ReworkBudget{})
	thenRejected(t, events, err, domain.ErrMissingValue)

	_, err = domain.NewReworkBudget(0)
	require.ErrorIs(t, err, domain.ErrInvalidReworkBudget)
}

func TestAbandonWork_InTheFixerOrAtTheReworkLimit(t *testing.T) {
	for name, given := range map[string][]domain.WorkEvent{
		"in the fixer":    join(onReview(), intoFixer(domain.StageReview, 1, 1)),
		"at rework limit": append(reworkedTimes(3), domain.ReworkLimitReached{WorkID: workID, Reworks: must(domain.NewReworkCount(3))}),
	} {
		t.Run(name, func(t *testing.T) {
			w := stateIs[domain.ActiveWork](t, givenWork(t, given...))

			events, err := w.AbandonWork(owner, reason)

			thenEvents(t, events, err, domain.WorkEvent(domain.WorkAbandoned{WorkID: workID, Owner: owner, Reason: reason}))
			stateIs[domain.AbandonedWork](t, thenWorkState(t, given, events))
		})
	}
}

func TestActiveWorkOn_CountsWorkInTheFixerAsActive(t *testing.T) {
	w, err := activeOn(t, mainBranch, join(onReview(), intoFixer(domain.StageReview, 1, 1))).Work()

	require.NoError(t, err)
	assert.Equal(t, domain.StageFixer, w.CurrentStage())
}

func TestRebuildWork_RejectsImpossibleReworkHistories(t *testing.T) {
	limit := domain.ReworkLimitReached{WorkID: workID, Reworks: must(domain.NewReworkCount(3))}
	cases := map[string][]domain.WorkEvent{
		"fixer entered without its stage":                append(onReview(), intoFixer(domain.StageReview, 1, 1)[0]),
		"fixer entered with another stage":               append(onReview(), intoFixer(domain.StageReview, 1, 1)[0], entered(domain.StageImplement, 2)),
		"fixer entered from a stage that is not current": join(onReview(), intoFixer(domain.StageIntegrationTesting, 1, 1)),
		"fixer entered from another visit":               join(onReview(), intoFixer(domain.StageReview, 2, 1)),
		"fixer entered from a code stage": join(append(started(), entered(domain.StageSpecify, 1), entered(domain.StageImplement, 1)),
			intoFixer(domain.StageImplement, 1, 1)),
		"fixer visit skipped": join(onReview(), intoFixer(domain.StageReview, 1, 2)),
		"fixer in performance mode from review": append(onReview(),
			domain.FixerEntered{WorkID: workID, From: domain.StageReview, FromVisit: visit1, Visit: visit1, Mode: domain.FixerModePerformance, Reason: reason},
			entered(domain.StageFixer, 1)),
		"fixer entered beyond the rework budget": join(reworkedTimes(3), intoFixer(domain.StageReview, 4, 4)),
		"rework limit before the budget is used": append(reworkedTimes(2), limit),
		"rework to a later stage":                join(onReview(), intoFixer(domain.StageReview, 1, 1), backTo(domain.StageIntegrationTesting, 1)),
		"rework to the same visit":               join(onReview(), intoFixer(domain.StageReview, 1, 1), backTo(domain.StageImplement, 1)),
		"rework without its stage":               append(join(onReview(), intoFixer(domain.StageReview, 1, 1)), backTo(domain.StageImplement, 2)[0]),
		"later stage entered on its first visit again": join(onReview(), intoFixer(domain.StageReview, 1, 1), backTo(domain.StageImplement, 2),
			[]domain.WorkEvent{entered(domain.StageReview, 1)}),
		"work advanced while in the fixer": append(join(onReview(), intoFixer(domain.StageReview, 1, 1)), entered(domain.StageIntegrationTesting, 1)),
		"rework budget extended wrongly": append(reworkedTimes(3), limit, domain.ReworkBudgetExtended{
			WorkID: workID, Owner: owner, Additional: must(domain.NewReworkBudget(1)), NewBudget: must(domain.NewReworkBudget(5)),
		}),
		"rework budget extended before the limit": append(reworkedTimes(1), domain.ReworkBudgetExtended{
			WorkID: workID, Owner: owner, Additional: must(domain.NewReworkBudget(1)), NewBudget: must(domain.NewReworkBudget(4)),
		}),
	}
	for name, history := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := domain.RebuildWork(history)
			require.ErrorIs(t, err, domain.ErrInconsistentHistory)
		})
	}
}
