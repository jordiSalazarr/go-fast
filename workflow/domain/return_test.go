package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// returned is the owner returning the work from a stage visit to a new visit
// of target.
func returned(from domain.Stage, fromVisit int, target domain.Stage, v int) []domain.WorkEvent {
	return []domain.WorkEvent{
		domain.WorkReturned{WorkID: workID, Owner: owner, From: from, FromVisit: visit(fromVisit), Target: target, Visit: visit(v), Reason: reasonOf("the specs miss refunds")},
		entered(target, v),
	}
}

func TestReturnTo_AnEarlierStage_EntersItsNextVisit(t *testing.T) {
	w := asInProgress(t, givenWork(t, onReview()...))
	assert.Equal(t, []domain.Stage{domain.StageDiscovery, domain.StageSpecify, domain.StageImplement}, w.ReturnTargets())

	events, err := w.ReturnTo(owner, domain.StageSpecify, reasonOf("the specs miss refunds"))

	thenEvents(t, events, err, returned(domain.StageReview, 1, domain.StageSpecify, 2)...)
	back := asInProgress(t, thenWorkState(t, onReview(), events))
	assert.Equal(t, domain.StageSpecify, back.CurrentStage())
	assert.Equal(t, visit(2), back.CurrentVisit())
	assert.Equal(t, 0, back.Reworks().Int(), "a return is not a rework")
}

func TestReturnTo_ClearsTheStressDecision(t *testing.T) {
	history := append(onReview(), decided(skipStress), entered(domain.StageIntegrationTesting, 1))
	w := asInProgress(t, givenWork(t, history...))

	events, err := w.ReturnTo(owner, domain.StageImplement, reasonOf("the specs miss refunds"))
	require.NoError(t, err)

	_, _, ok := asInProgress(t, thenWorkState(t, history, events)).StressDecision()
	assert.False(t, ok, "the next review decides again")
}

func TestReturnTo_ALaterStageOrTheCurrentOne_IsRefusedNamingTheTargets(t *testing.T) {
	w := asInProgress(t, givenWork(t, onReview()...))
	for _, target := range []domain.Stage{domain.StageReview, domain.StageIntegrationTesting, domain.StageStressTesting, domain.StageFixer} {
		_, err := w.ReturnTo(owner, target, reasonOf("again"))

		var invalid *domain.InvalidReturnError
		require.ErrorAs(t, err, &invalid, target.String())
		assert.Equal(t, []domain.Stage{domain.StageDiscovery, domain.StageSpecify, domain.StageImplement}, invalid.Allowed)
		assert.ErrorIs(t, err, domain.ErrInvalidReturn)
	}
}

func TestReturnTo_FromTheFirstStage_HasNoTarget(t *testing.T) {
	w := asInProgress(t, givenWork(t, started()...))
	assert.Empty(t, w.ReturnTargets())

	_, err := w.ReturnTo(owner, domain.StageDiscovery, reasonOf("again"))
	assert.ErrorIs(t, err, domain.ErrInvalidReturn)
}

func TestReturnTo_NeedsTheOwnerAndAReason(t *testing.T) {
	w := asInProgress(t, givenWork(t, onReview()...))
	_, err := w.ReturnTo(domain.Owner{}, domain.StageSpecify, reasonOf("again"))
	assert.ErrorIs(t, err, domain.ErrMissingValue)
	_, err = w.ReturnTo(owner, domain.StageSpecify, domain.Reason{})
	assert.ErrorIs(t, err, domain.ErrMissingValue)
}

func TestRebuildWork_AReturnThatTheWorkCouldNotMake_IsInconsistent(t *testing.T) {
	for name, history := range map[string][]domain.WorkEvent{
		"to a later stage":         join(onReview(), returned(domain.StageReview, 1, domain.StageIntegrationTesting, 1)),
		"from another visit":       join(onReview(), returned(domain.StageReview, 2, domain.StageSpecify, 2)),
		"to the wrong visit":       join(onReview(), returned(domain.StageReview, 1, domain.StageSpecify, 3)),
		"without its StageEntered": join(onReview(), returned(domain.StageReview, 1, domain.StageSpecify, 2)[:1]),
	} {
		_, err := domain.RebuildWork(history)
		assert.ErrorIs(t, err, domain.ErrInconsistentHistory, name)
	}
}

// The owner can return work whose current stage is escalated: the domain
// returns it whatever state the visit is in, and the escalated assignment
// can be cancelled.
func TestReturnTo_FromAnEscalatedStage(t *testing.T) {
	w := asInProgress(t, givenWork(t, onReview()...))
	events, err := w.ReturnTo(owner, domain.StageSpecify, reasonOf("the specs miss refunds"))
	thenEvents(t, events, err, returned(domain.StageReview, 1, domain.StageSpecify, 2)...)

	review := domain.AssignmentIDFor(workID, domain.StageReview, visit(1))
	escalated, err := domain.RebuildAssignment([]domain.AssignmentEvent{
		domain.AssignmentOpened{AssignmentID: review, WorkID: workID, Stage: domain.StageReview, Visit: visit(1), Gate: domain.GateAuto, Budget: must(domain.NewAttemptBudget(1))},
		domain.AttemptFailed{AssignmentID: review, Attempt: must(domain.NewAttempt(1)), Reason: reasonOf("x")},
		domain.AssignmentEscalated{AssignmentID: review, AttemptsUsed: must(domain.NewAttemptCount(1)), Budget: must(domain.NewAttemptBudget(1))},
	})
	require.NoError(t, err)
	_, err = escalated.(domain.Escalated).Cancel(reasonOf("work returned to specify"))
	assert.NoError(t, err)
}

// At its rework limit, the owner can send the work back instead of
// extending the rework budget; the budget stays as it was.
func TestReturnTo_FromTheReworkLimit(t *testing.T) {
	given := append(reworkedTimes(3), domain.ReworkLimitReached{WorkID: workID, Reworks: must(domain.NewReworkCount(3))})
	limit := stateIs[domain.ReworkLimitReachedWork](t, givenWork(t, given...))
	assert.Equal(t, []domain.Stage{domain.StageDiscovery, domain.StageSpecify, domain.StageImplement}, limit.ReturnTargets())

	events, err := limit.ReturnTo(owner, domain.StageSpecify, reasonOf("the specs miss refunds"))

	thenEvents(t, events, err, returned(domain.StageReview, 4, domain.StageSpecify, 2)...)
	back := asInProgress(t, thenWorkState(t, given, events))
	assert.Equal(t, domain.StageSpecify, back.CurrentStage())
	assert.Equal(t, 3, back.Reworks().Int())
	assert.Equal(t, domain.DefaultReworkBudget(), back.ReworkBudget(), "no rework budget used or added")
}

// From the fixer, the owner sends the work to a path stage before the one
// that handed it over; the fixer visit is left behind (and cancelled).
func TestReturnTo_FromTheFixer(t *testing.T) {
	fixer := toFixerFrom(t, domain.WorkTypeFixBug, domain.PathV1, domain.StageReview)
	assert.Equal(t, []domain.Stage{domain.StageDiscovery, domain.StageSpecify, domain.StageImplement}, fixer.ReturnTargets())

	events, err := fixer.ReturnTo(owner, domain.StageDiscovery, reasonOf("the bug is elsewhere"))
	require.NoError(t, err)
	assert.Equal(t, domain.WorkReturned{WorkID: workID, Owner: owner, From: domain.StageFixer, FromVisit: visit(1), Target: domain.StageDiscovery,
		Visit: visit(2), Reason: reasonOf("the bug is elsewhere")}, events[0])

	_, err = fixer.ReturnTo(owner, domain.StageReview, reasonOf("x"))
	assert.ErrorIs(t, err, domain.ErrInvalidReturn, "not the stage that handed over, nor any later one")
	_, err = fixer.ReturnTo(owner, domain.StageFixer, reasonOf("x"))
	assert.ErrorIs(t, err, domain.ErrInvalidReturn)
}

// On the new paths, any entered earlier stage is a return target: grill
// and plan on a refactor.
func TestReturnTargets_OnTheNewPaths(t *testing.T) {
	history := must(domain.StartWork(workID, domain.WorkTypeRefactor, description, mainBranch, activeOn(t, mainBranch)))
	for range 3 {
		history = advance(t, history)
	}
	w := asInProgress(t, givenWork(t, history...))
	require.Equal(t, domain.StageCharacterize, w.CurrentStage())
	assert.Equal(t, []domain.Stage{domain.StageDiscovery, domain.StageGrill, domain.StagePlan}, w.ReturnTargets())
	events, err := w.ReturnTo(owner, domain.StagePlan, reasonOf("the plan misses the ledger"))
	require.NoError(t, err)
	back := asInProgress(t, givenWork(t, append(history, events...)...))
	assert.Equal(t, domain.StagePlan, back.CurrentStage())
	assert.Equal(t, visit(2), back.CurrentVisit())
	assert.Equal(t, 0, back.Reworks().Int())
}
