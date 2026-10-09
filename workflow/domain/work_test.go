package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func started() []domain.WorkEvent {
	return []domain.WorkEvent{
		domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: description},
		domain.StageEntered{WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
	}
}

// completed is the history of a work that went through the whole fix-bug path.
func completed() []domain.WorkEvent {
	h := started()
	for _, st := range []struct {
		stage domain.Stage
		gate  domain.Gate
	}{
		{domain.StageSpecify, domain.GateHuman},
		{domain.StageImplement, domain.GateAuto},
		{domain.StageReview, domain.GateAuto},
		{domain.StageIntegrationTesting, domain.GateAuto},
	} {
		h = append(h, domain.StageEntered{WorkID: workID, Stage: st.stage, Visit: visit1, Gate: st.gate, Budget: budget3})
	}
	return append(h, domain.WorkCompleted{WorkID: workID})
}

func asInProgress(t *testing.T, state domain.WorkState) domain.InProgressWork {
	t.Helper()
	return stateIs[domain.InProgressWork](t, state)
}

func TestStartWork_WithNoActiveWork_StartsOnFirstStage(t *testing.T) {
	events, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, domain.NoActiveWork())

	thenEvents(t, events, err, started()...)
	w := asInProgress(t, thenWorkState(t, nil, events))
	assert.Equal(t, domain.StageDiscovery, w.CurrentStage())
	assert.Equal(t, visit1, w.CurrentVisit())
}

func TestStartWork_WhenWorkIsActive_IsRejectedNamingTheActiveWork(t *testing.T) {
	activeDescription := must(domain.NewDescription("crash on save"))
	active := domain.ActiveWork(otherWorkID, domain.WorkTypeFixBug, activeDescription)

	events, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, active)

	thenRejected(t, events, err, domain.ErrWorkAlreadyActive)
	var activeErr *domain.WorkAlreadyActiveError
	require.ErrorAs(t, err, &activeErr)
	assert.Equal(t, otherWorkID, activeErr.ID)
	assert.Equal(t, activeDescription, activeErr.Description)
	assert.Contains(t, err.Error(), "crash on save")
	assert.Contains(t, err.Error(), "w0")
}

func TestStartWork_WithoutAnActiveWorkFact_IsRejected(t *testing.T) {
	events, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, domain.ActiveWorkFact{})

	thenRejected(t, events, err, domain.ErrMissingValue)
}

func TestAdvancePastStage_WalksTheWholeFixBugPathToCompletion(t *testing.T) {
	history := started()
	want := []struct {
		stage domain.Stage
		gate  domain.Gate
	}{
		{domain.StageSpecify, domain.GateHuman},
		{domain.StageImplement, domain.GateAuto},
		{domain.StageReview, domain.GateAuto},
		{domain.StageIntegrationTesting, domain.GateAuto},
	}

	for _, next := range want {
		current := asInProgress(t, givenWork(t, history...))

		events, err := current.AdvancePastStage(current.CurrentStage(), visit1)

		thenEvents(t, events, err, domain.WorkEvent(domain.StageEntered{
			WorkID: workID, Stage: next.stage, Visit: visit1, Gate: next.gate, Budget: budget3,
		}))
		history = append(history, events...)
	}

	last := asInProgress(t, givenWork(t, history...))
	events, err := last.AdvancePastStage(domain.StageIntegrationTesting, visit1)

	thenEvents(t, events, err, domain.WorkEvent(domain.WorkCompleted{WorkID: workID}))
	stateIs[domain.CompletedWork](t, thenWorkState(t, history, events))
}

func TestAdvancePastStage_ThatIsNotCurrent_IsRejected(t *testing.T) {
	w := asInProgress(t, givenWork(t, started()...))

	events, err := w.AdvancePastStage(domain.StageImplement, visit1)

	thenRejected(t, events, err, domain.ErrNotCurrentStage)
	var stageErr *domain.NotCurrentStageError
	require.ErrorAs(t, err, &stageErr)
	assert.Equal(t, domain.StageDiscovery, stageErr.Current)
}

func TestAdvancePastStage_WithAnotherVisit_IsRejected(t *testing.T) {
	w := asInProgress(t, givenWork(t, started()...))

	events, err := w.AdvancePastStage(domain.StageDiscovery, must(domain.NewVisit(2)))

	thenRejected(t, events, err, domain.ErrNotCurrentStage)
}

func TestAbandonWork(t *testing.T) {
	w := asInProgress(t, givenWork(t, started()...))

	events, err := w.AbandonWork(owner, reason)

	thenEvents(t, events, err, domain.WorkEvent(domain.WorkAbandoned{WorkID: workID, Owner: owner, Reason: reason}))
	abandoned := stateIs[domain.AbandonedWork](t, thenWorkState(t, started(), events))
	assert.Equal(t, owner, abandoned.Owner())
}

func TestAbandonWork_WithoutOwner_IsRejected(t *testing.T) {
	w := asInProgress(t, givenWork(t, started()...))

	events, err := w.AbandonWork(domain.Owner{}, reason)

	thenRejected(t, events, err, domain.ErrMissingValue)
}

// Completed and abandoned work accept no commands: their state types have no
// command methods, so the compiler enforces it. These tests check that the
// history rebuilds into those types.
func TestCompletedAndAbandonedWork_AcceptNoFurtherCommands(t *testing.T) {
	abandoned := givenWork(t, append(started(), domain.WorkAbandoned{WorkID: workID, Owner: owner, Reason: reason})...)
	_, isInProgress := abandoned.(domain.InProgressWork)
	assert.False(t, isInProgress)
	stateIs[domain.AbandonedWork](t, abandoned)

	done := givenWork(t, completed()...)
	_, isInProgress = done.(domain.InProgressWork)
	assert.False(t, isInProgress)
	stateIs[domain.CompletedWork](t, done)
}

func TestRebuildWork_RejectsInconsistentHistory(t *testing.T) {
	cases := map[string][]domain.WorkEvent{
		"empty":                 nil,
		"no WorkStarted first":  {domain.WorkCompleted{WorkID: workID}},
		"started without stage": started()[:1],
		"stage skipped": append(started()[:1], domain.StageEntered{
			WorkID: workID, Stage: domain.StageImplement, Visit: visit1, Gate: domain.GateAuto, Budget: budget3,
		}),
		"completed before last stage": append(started(), domain.WorkCompleted{WorkID: workID}),
		"event after completion":      append(completed(), domain.WorkCompleted{WorkID: workID}),
		"event of another work":       append(started(), domain.WorkCompleted{WorkID: otherWorkID}),
	}
	for name, history := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := domain.RebuildWork(history)
			require.ErrorIs(t, err, domain.ErrInconsistentHistory)
		})
	}
}

func TestActiveWorkAmong(t *testing.T) {
	inProgress := givenWork(t, started()...)
	done := givenWork(t, completed()...)

	assert.Equal(t, domain.NoActiveWork(), domain.ActiveWorkAmong(nil))
	assert.Equal(t, domain.NoActiveWork(), domain.ActiveWorkAmong([]domain.WorkState{done}))
	assert.Equal(t,
		domain.ActiveWork(workID, domain.WorkTypeFixBug, description),
		domain.ActiveWorkAmong([]domain.WorkState{done, inProgress}))
}

func TestActiveWorkIn(t *testing.T) {
	_, err := domain.ActiveWorkIn([]domain.WorkState{givenWork(t, completed()...)})
	require.ErrorIs(t, err, domain.ErrNoActiveWork)

	w, err := domain.ActiveWorkIn([]domain.WorkState{givenWork(t, started()...)})
	require.NoError(t, err)
	assert.Equal(t, workID, w.ID())
	assert.Equal(t, domain.AssignmentIDFor(workID, domain.StageDiscovery, visit1), w.CurrentAssignment())
}
