package advanceworkonacceptance

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

var (
	workID  = must(domain.NewWorkID("w1"))
	budget3 = must(domain.NewAttemptBudget(3))
	visit1  = domain.FirstVisit()
	started = []domain.WorkEvent{
		domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: must(domain.NewDescription("bug"))},
		domain.StageEntered{WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
	}
	specifyEntered = domain.StageEntered{WorkID: workID, Stage: domain.StageSpecify, Visit: visit1, Gate: domain.GateHuman, Budget: budget3}
	discoveryID    = domain.AssignmentIDFor(workID, domain.StageDiscovery, visit1)
	accepted       = domain.AssignmentAccepted{AssignmentID: discoveryID, WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1}
)

func work(t *testing.T, events ...domain.WorkEvent) domain.WorkState {
	t.Helper()
	w, err := domain.RebuildWork(events)
	require.NoError(t, err)
	return w
}

func TestWorkToAdvance_OnTheAcceptedStage_Advances(t *testing.T) {
	w, ok := workToAdvance(accepted, work(t, started...))

	require.True(t, ok)
	assert.Equal(t, domain.StageDiscovery, w.CurrentStage())
}

func TestWorkToAdvance_AlreadyPastTheStage_DoesNothing(t *testing.T) {
	_, ok := workToAdvance(accepted, work(t, append(started, specifyEntered)...))

	assert.False(t, ok)
}

func TestWorkToAdvance_WorkNoLongerInProgress_DoesNothing(t *testing.T) {
	abandoned := domain.WorkAbandoned{WorkID: workID, Owner: must(domain.NewOwner("Jordi", "")), Reason: must(domain.NewReason("x"))}

	_, ok := workToAdvance(accepted, work(t, append(started, abandoned)...))

	assert.False(t, ok)
}

func TestRun_AdvancesOnceAndIsIdempotent(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, eventlog.Events(started)...))
		opened := domain.AssignmentOpened{AssignmentID: discoveryID, WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateAuto, Budget: budget3}
		require.NoError(t, s.Append(eventlog.AssignmentStream(discoveryID), 0, agent, opened, accepted))

		first, err := Run(s)
		require.NoError(t, err)
		second, err := Run(s)
		require.NoError(t, err)

		assert.Equal(t, 1, first)
		assert.Equal(t, 0, second)
		records, _ := s.ReadAll()
		assert.Equal(t, specifyEntered, records[len(records)-1].Event)
		return nil
	}))
}
