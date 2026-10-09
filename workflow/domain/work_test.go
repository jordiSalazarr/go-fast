package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func started() []domain.WorkEvent {
	return []domain.WorkEvent{
		domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: description, Branch: mainBranch},
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

// activeOn is the active work fact of a branch, given these works' histories.
func activeOn(t *testing.T, branch domain.Branch, histories ...[]domain.WorkEvent) domain.ActiveWorkFact {
	t.Helper()
	var works []domain.WorkState
	for _, h := range histories {
		works = append(works, givenWork(t, h...))
	}
	fact, err := domain.ActiveWorkOn(branch, works)
	require.NoError(t, err)
	return fact
}

// startedOn is the history of another work started on a branch.
func startedOn(id domain.WorkID, branch domain.Branch, desc domain.Description) []domain.WorkEvent {
	return []domain.WorkEvent{
		domain.WorkStarted{WorkID: id, WorkType: domain.WorkTypeFixBug, Description: desc, Branch: branch},
		domain.StageEntered{WorkID: id, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
	}
}

func TestStartWork_WithNoActiveWork_StartsOnFirstStage(t *testing.T) {
	events, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, mainBranch, activeOn(t, mainBranch))

	thenEvents(t, events, err, started()...)
	w := asInProgress(t, thenWorkState(t, nil, events))
	assert.Equal(t, domain.StageDiscovery, w.CurrentStage())
	assert.Equal(t, visit1, w.CurrentVisit())
	assert.Equal(t, mainBranch, w.Branch())
}

func TestStartWork_WhenWorkIsActiveOnTheSameBranch_IsRejectedNamingTheActiveWork(t *testing.T) {
	activeDescription := must(domain.NewDescription("crash on save"))
	active := activeOn(t, mainBranch, startedOn(otherWorkID, mainBranch, activeDescription))

	events, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, mainBranch, active)

	thenRejected(t, events, err, domain.ErrWorkAlreadyActive)
	var activeErr *domain.WorkAlreadyActiveError
	require.ErrorAs(t, err, &activeErr)
	assert.Equal(t, otherWorkID, activeErr.ID)
	assert.Equal(t, activeDescription, activeErr.Description)
	assert.Equal(t, mainBranch, activeErr.Branch)
	assert.Contains(t, err.Error(), "crash on save")
	assert.Contains(t, err.Error(), "w0")
}

func TestStartWork_WhenWorkIsActiveOnAnotherBranch_IsAllowed(t *testing.T) {
	active := activeOn(t, mainBranch, startedOn(otherWorkID, otherBranch, must(domain.NewDescription("crash on save"))))

	events, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, mainBranch, active)

	thenEvents(t, events, err, started()...)
}

func TestStartWork_WithTheFactOfAnotherBranch_IsRejected(t *testing.T) {
	events, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, mainBranch, activeOn(t, otherBranch))

	thenRejected(t, events, err, domain.ErrMissingValue)
}

func TestStartWork_WithoutAnActiveWorkFact_IsRejected(t *testing.T) {
	events, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, mainBranch, domain.ActiveWorkFact{})

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
		"empty":                  nil,
		"no WorkStarted first":   {domain.WorkCompleted{WorkID: workID}},
		"started without branch": append([]domain.WorkEvent{domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: description}}, started()[1]),
		"started without stage":  started()[:1],
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

func TestActiveWorkOn(t *testing.T) {
	done := completed()
	inProgress := started()
	elsewhere := startedOn(otherWorkID, otherBranch, must(domain.NewDescription("crash on save")))

	_, err := activeOn(t, mainBranch).Work()
	require.ErrorIs(t, err, domain.ErrNoActiveWork)
	_, err = activeOn(t, mainBranch, done, elsewhere).Work()
	require.ErrorIs(t, err, domain.ErrNoActiveWork, "completed work and work on other branches are not active here")

	w, err := activeOn(t, mainBranch, done, elsewhere, inProgress).Work()
	require.NoError(t, err)
	assert.Equal(t, workID, w.ID())
	assert.Equal(t, domain.AssignmentIDFor(workID, domain.StageDiscovery, visit1), w.CurrentAssignment())

	w, err = activeOn(t, otherBranch, inProgress, elsewhere).Work()
	require.NoError(t, err)
	assert.Equal(t, otherWorkID, w.ID())
}

func TestActiveWorkOn_TwoWorksInProgressOnOneBranch_IsInconsistent(t *testing.T) {
	works := []domain.WorkState{
		givenWork(t, started()...),
		givenWork(t, startedOn(otherWorkID, mainBranch, must(domain.NewDescription("crash on save")))...),
	}

	_, err := domain.ActiveWorkOn(mainBranch, works)

	require.ErrorIs(t, err, domain.ErrInconsistentHistory)
	var conflict *domain.ConflictingActiveWorkError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, []domain.WorkID{workID, otherWorkID}, conflict.Works)
	assert.Contains(t, err.Error(), "w1, w0")
}

func TestActiveWorkOn_WithoutABranch_IsRejected(t *testing.T) {
	_, err := domain.ActiveWorkOn(domain.Branch{}, nil)

	require.ErrorIs(t, err, domain.ErrMissingValue)
}
