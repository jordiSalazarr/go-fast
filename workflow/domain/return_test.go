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
