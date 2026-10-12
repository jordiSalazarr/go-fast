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
		domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: must(domain.NewDescription("bug")), Branch: must(domain.NewBranch("main"))},
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

		first, err := Run(s, nil)
		require.NoError(t, err)
		second, err := Run(s, nil)
		require.NoError(t, err)

		assert.Equal(t, 1, first)
		assert.Equal(t, 0, second)
		records, _ := s.ReadAll()
		assert.Equal(t, specifyEntered, records[len(records)-1].Event)
		return nil
	}))
}

// The boundary sees the accepted visit before the work moves on: the next
// visit is not entered yet, so its baseline can't have been taken.
func TestRun_TellsTheBoundaryBeforeEnteringTheNextVisit(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, eventlog.Events(started)...))
		opened := domain.AssignmentOpened{AssignmentID: discoveryID, WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateAuto, Budget: budget3}
		require.NoError(t, s.Append(eventlog.AssignmentStream(discoveryID), 0, agent, opened, accepted))

		var told []domain.Stage
		boundary := func(h eventlog.History) {
			w, _, err := h.Work(workID)
			require.NoError(t, err)
			told = append(told, w.(domain.InProgressWork).CurrentStage())
		}
		_, err := Run(s, boundary)
		require.NoError(t, err)

		assert.Equal(t, []domain.Stage{domain.StageDiscovery}, told, "told once, while the work is still on the accepted visit")
		return nil
	}))
}

// countingLog counts how often the automation reads the log.
type countingLog struct {
	Log
	reads int
}

func (c *countingLog) ReadAll() ([]eventlog.Recorded, error) {
	c.reads++
	return c.Log.ReadAll()
}

func TestRun_ReadsTheLogOnceAndAgainOnlyAfterAppending(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, eventlog.Events(started)...))
		opened := domain.AssignmentOpened{AssignmentID: discoveryID, WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateAuto, Budget: budget3}
		require.NoError(t, s.Append(eventlog.AssignmentStream(discoveryID), 0, agent, opened, accepted))

		first := &countingLog{Log: s}
		_, err := Run(first, nil)
		require.NoError(t, err)
		again := &countingLog{Log: s}
		_, err = Run(again, nil)
		require.NoError(t, err)

		assert.Equal(t, 2, first.reads, "one read, one after the append")
		assert.Equal(t, 1, again.reads, "nothing to do: one read")
		return nil
	}))
}

// Review's acceptance carries its stress decision: advancing past review
// records it, and only then enters integration testing. Running again adds
// nothing.
func TestRun_PastReview_RecordsItsStressDecision_AndIsIdempotent(t *testing.T) {
	include := must(domain.NewStressChoice(domain.StressInclude, must(domain.NewReason("touches the ledger")),
		[]domain.NFRID{must(domain.NewNFRID("wallet-latency"))}))
	reviewID := domain.AssignmentIDFor(workID, domain.StageReview, visit1)
	onReview := append(append([]domain.WorkEvent(nil), started...), specifyEntered,
		domain.StageEntered{WorkID: workID, Stage: domain.StageImplement, Visit: visit1, Gate: domain.GateAuto, Budget: budget3},
		domain.StageEntered{WorkID: workID, Stage: domain.StageReview, Visit: visit1, Gate: domain.GateAuto, Budget: budget3})
	store := must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, eventlog.Events(onReview)...))
		require.NoError(t, s.Append(eventlog.AssignmentStream(reviewID), 0, agent,
			domain.AssignmentOpened{AssignmentID: reviewID, WorkID: workID, Stage: domain.StageReview, Visit: visit1, Gate: domain.GateAuto, Budget: budget3},
			domain.AssignmentAccepted{AssignmentID: reviewID, WorkID: workID, Stage: domain.StageReview, Visit: visit1, Stress: include}))

		first, err := Run(s, nil)
		require.NoError(t, err)
		second, err := Run(s, nil)
		require.NoError(t, err)

		assert.Equal(t, 2, first)
		assert.Equal(t, 0, second)
		records, _ := s.ReadAll()
		assert.Equal(t, []domain.Event{
			domain.StressDecided{WorkID: workID, Decision: domain.StressInclude, Reason: include.Reason(), ForcedBy: include.ForcedBy()},
			domain.StageEntered{WorkID: workID, Stage: domain.StageIntegrationTesting, Visit: visit1, Gate: domain.GateAuto, Budget: budget3},
		}, []domain.Event{records[len(records)-2].Event, records[len(records)-1].Event})
		return nil
	}))
}
