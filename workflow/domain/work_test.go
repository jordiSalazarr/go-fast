package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func started() []domain.WorkEvent {
	return []domain.WorkEvent{
		domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, PathVersion: domain.PathV1, Description: description, Branch: mainBranch},
		domain.StageEntered{WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
	}
}

// completed is the history of a work that went through the whole fix-bug
// path, skipping stress testing.
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
		if st.stage == domain.StageReview {
			h = append(h, decided(skipStress))
		}
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

		events, err := current.AdvancePastStage(current.CurrentStage(), visit1, stressFor(current.CurrentStage()))

		wantEvents := []domain.WorkEvent{domain.StageEntered{WorkID: workID, Stage: next.stage, Visit: visit1, Gate: next.gate, Budget: budget3}}
		if current.CurrentStage() == domain.StageReview {
			wantEvents = append([]domain.WorkEvent{decided(skipStress)}, wantEvents...)
		}
		thenEvents(t, events, err, wantEvents...)
		history = append(history, events...)
	}

	last := asInProgress(t, givenWork(t, history...))
	events, err := last.AdvancePastStage(domain.StageIntegrationTesting, visit1, domain.StressChoice{})

	thenEvents(t, events, err, domain.WorkEvent(domain.WorkCompleted{WorkID: workID}))
	stateIs[domain.CompletedWork](t, thenWorkState(t, history, events))
}

func TestAdvancePastStage_ThatIsNotCurrent_IsRejected(t *testing.T) {
	w := asInProgress(t, givenWork(t, started()...))

	events, err := w.AdvancePastStage(domain.StageImplement, visit1, domain.StressChoice{})

	thenRejected(t, events, err, domain.ErrNotCurrentStage)
	var stageErr *domain.NotCurrentStageError
	require.ErrorAs(t, err, &stageErr)
	assert.Equal(t, domain.StageDiscovery, stageErr.Current)
}

func TestAdvancePastStage_WithAnotherVisit_IsRejected(t *testing.T) {
	w := asInProgress(t, givenWork(t, started()...))

	events, err := w.AdvancePastStage(domain.StageDiscovery, must(domain.NewVisit(2)), domain.StressChoice{})

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

// advance moves the work past its current stage visit, and returns the
// history with the events it recorded.
func advance(t *testing.T, history []domain.WorkEvent) []domain.WorkEvent {
	t.Helper()
	w := asInProgress(t, givenWork(t, history...))
	events, err := w.AdvancePastStage(w.CurrentStage(), w.CurrentVisit(), domain.StressChoice{})
	require.NoError(t, err)
	return append(history, events...)
}

// Given a feature work type, when work starts, then it follows path v3:
// without an event model, discovery (auto gate), grill (human gate), design
// (human gate), then the rest of the path.
func TestStartWork_OfAFeatureType_FollowsPathV3(t *testing.T) {
	for _, workType := range []domain.WorkType{domain.WorkTypeNewFeature, domain.WorkTypeModifyBehaviour} {
		history := must(domain.StartWork(workID, workType, must(domain.NewDescription("wallets")), mainBranch, activeOn(t, mainBranch)))
		assert.Equal(t, domain.PathV3, history[0].(domain.WorkStarted).PathVersion, workType.String())
		w := asInProgress(t, givenWork(t, history...))
		assert.Equal(t, domain.StageDiscovery, w.CurrentStage())
		assert.Equal(t, domain.GateAuto, w.CurrentStep().Gate())
		assert.Equal(t, domain.AssignmentIDFor(workID, domain.StageDiscovery, visit1), w.FirstAssignment())
		assert.True(t, w.Skips(domain.StageEventModelling))

		var stages []domain.Stage
		var gates []domain.Gate
		for range 3 {
			history = advance(t, history)
			entered := history[len(history)-1].(domain.StageEntered)
			stages, gates = append(stages, entered.Stage), append(gates, entered.Gate)
		}
		assert.Equal(t, []domain.Stage{domain.StageGrill, domain.StageDesign, domain.StageSpecify}, stages, workType.String())
		assert.Equal(t, []domain.Gate{domain.GateHuman, domain.GateHuman, domain.GateHuman}, gates, workType.String())
	}
}

// Given a feature work started on path v2, when it is rebuilt and advanced,
// then it finishes on v2: grill, then discovery, then design.
func TestAWorkStartedOnPathV2_FinishesOnV2(t *testing.T) {
	for _, workType := range []domain.WorkType{domain.WorkTypeNewFeature, domain.WorkTypeModifyBehaviour} {
		history := []domain.WorkEvent{
			domain.WorkStarted{WorkID: workID, WorkType: workType, PathVersion: domain.PathV2, Description: description, Branch: mainBranch},
			domain.StageEntered{WorkID: workID, Stage: domain.StageGrill, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
		}
		w := asInProgress(t, givenWork(t, history...))
		assert.Equal(t, domain.PathV2, w.Path().Version(), workType.String())
		var stages []domain.Stage
		for range 2 {
			history = advance(t, history)
			stages = append(stages, history[len(history)-1].(domain.StageEntered).Stage)
		}
		assert.Equal(t, []domain.Stage{domain.StageDiscovery, domain.StageDesign}, stages, workType.String())
	}
}

// Given a feature work started before paths had versions, when it is
// rebuilt, then it keeps path v1: discovery (human gate), design, specify.
func TestAWorkStartedBeforePathVersions_KeepsPathV1(t *testing.T) {
	for _, workType := range []domain.WorkType{domain.WorkTypeNewFeature, domain.WorkTypeModifyBehaviour} {
		history := []domain.WorkEvent{
			domain.WorkStarted{WorkID: workID, WorkType: workType, Description: description, Branch: mainBranch},
			domain.StageEntered{WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
		}
		w := asInProgress(t, givenWork(t, history...))
		assert.Equal(t, domain.PathV1, w.Path().Version(), workType.String())
		assert.Equal(t, domain.AssignmentIDFor(workID, domain.StageDiscovery, visit1), w.FirstAssignment())
		assert.False(t, w.Path().Includes(domain.StageGrill))

		history = advance(t, history)
		assert.Equal(t, domain.StageEntered{WorkID: workID, Stage: domain.StageDesign, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
			history[len(history)-1], workType.String())
		history = advance(t, history)
		assert.Equal(t, domain.StageSpecify, history[len(history)-1].(domain.StageEntered).Stage, workType.String())

		// Its v1 history doesn't fit path v2, nor v2's first stage path v1.
		v2 := append([]domain.WorkEvent{domain.WorkStarted{WorkID: workID, WorkType: workType, PathVersion: domain.PathV2, Description: description, Branch: mainBranch}}, history[1:]...)
		_, err := domain.RebuildWork(v2)
		assert.ErrorIs(t, err, domain.ErrInconsistentHistory)
	}
}
