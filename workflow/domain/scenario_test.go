package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// Given / when / then helpers.
//
//	state := givenWork(t, pastEvents...)        // given
//	events, err := asInProgress(t, state).X(...) // when
//	thenEvents(t, events, err, newEvents...)     // then

func givenWork(t *testing.T, history ...domain.WorkEvent) domain.WorkState {
	t.Helper()
	state, err := domain.RebuildWork(history)
	require.NoError(t, err, "given history must rebuild")
	return state
}

func givenAssignment(t *testing.T, history ...domain.AssignmentEvent) domain.AssignmentState {
	t.Helper()
	state, err := domain.RebuildAssignment(history)
	require.NoError(t, err, "given history must rebuild")
	return state
}

func thenEvents[E any](t *testing.T, got []E, err error, want ...E) {
	t.Helper()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func thenRejected[E any](t *testing.T, got []E, err error, target error) {
	t.Helper()
	require.ErrorIs(t, err, target)
	assert.Empty(t, got)
}

// stateIs asserts the concrete state type and returns it.
func stateIs[S any](t *testing.T, state any) S {
	t.Helper()
	s, ok := state.(S)
	require.Truef(t, ok, "state is %T, want %T", state, *new(S))
	return s
}

// then rebuilds the full history (given + new events) and returns the state.
func thenWorkState(t *testing.T, given []domain.WorkEvent, newEvents []domain.WorkEvent) domain.WorkState {
	t.Helper()
	return givenWork(t, append(append([]domain.WorkEvent(nil), given...), newEvents...)...)
}

func thenAssignmentState(t *testing.T, given []domain.AssignmentEvent, newEvents []domain.AssignmentEvent) domain.AssignmentState {
	t.Helper()
	return givenAssignment(t, append(append([]domain.AssignmentEvent(nil), given...), newEvents...)...)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Fixtures.

var (
	workID      = must(domain.NewWorkID("w1"))
	otherWorkID = must(domain.NewWorkID("w0"))
	description = must(domain.NewDescription("login button does nothing"))
	owner       = must(domain.NewOwner("Jordi", "jordi@example.com"))
	reason      = must(domain.NewReason("tests fail"))
	feedback    = must(domain.NewFeedback("missing edge case"))
	budget3     = must(domain.NewAttemptBudget(3))
	visit1      = domain.FirstVisit()
)

func attempt(n int) domain.Attempt       { return must(domain.NewAttempt(n)) }
func attempts(n int) domain.AttemptCount { return must(domain.NewAttemptCount(n)) }
func budget(n int) domain.AttemptBudget  { return must(domain.NewAttemptBudget(n)) }
func reasonOf(s string) domain.Reason    { return must(domain.NewReason(s)) }
