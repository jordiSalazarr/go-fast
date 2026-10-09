package cancelassignmentonabandon

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
	workID       = must(domain.NewWorkID("w1"))
	budget3      = must(domain.NewAttemptBudget(3))
	visit1       = domain.FirstVisit()
	assignmentID = domain.AssignmentIDFor(workID, domain.StageImplement, visit1)
	opened       = domain.AssignmentOpened{AssignmentID: assignmentID, WorkID: workID, Stage: domain.StageImplement, Visit: visit1, Gate: domain.GateAuto, Budget: budget3}
	abandoned    = domain.WorkAbandoned{WorkID: workID, Owner: must(domain.NewOwner("Jordi", "")), Reason: must(domain.NewReason("wrong approach"))}
)

func assignment(t *testing.T, events ...domain.AssignmentEvent) domain.AssignmentState {
	t.Helper()
	a, err := domain.RebuildAssignment(events)
	require.NoError(t, err)
	return a
}

func TestAssignmentToCancel_OpenAssignment_IsCancelled(t *testing.T) {
	c, reason, ok := assignmentToCancel(abandoned, assignment(t, opened))

	require.True(t, ok)
	assert.IsType(t, domain.Open{}, c)
	assert.Equal(t, "work abandoned: wrong approach", reason.String())
}

func TestAssignmentToCancel_AlreadyCancelledOrAccepted_DoesNothing(t *testing.T) {
	cancelled := domain.AssignmentCancelled{AssignmentID: assignmentID, Reason: must(domain.NewReason("x"))}
	accepted := domain.AssignmentAccepted{AssignmentID: assignmentID, WorkID: workID, Stage: domain.StageImplement, Visit: visit1}

	_, _, ok := assignmentToCancel(abandoned, assignment(t, opened, cancelled))
	assert.False(t, ok)
	_, _, ok = assignmentToCancel(abandoned, assignment(t, opened, accepted))
	assert.False(t, ok)
}

func TestRun_CancelsOnceAndIsIdempotent(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")
	work := []domain.Event{
		domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: must(domain.NewDescription("bug")), Branch: must(domain.NewBranch("main"))},
		domain.StageEntered{WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
		abandoned,
	}

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, work...))
		require.NoError(t, s.Append(eventlog.AssignmentStream(assignmentID), 0, agent, opened))

		first, err := Run(s)
		require.NoError(t, err)
		second, err := Run(s)
		require.NoError(t, err)

		assert.Equal(t, 1, first)
		assert.Equal(t, 0, second)
		records, _ := s.ReadAll()
		assert.Equal(t, domain.AssignmentCancelled{AssignmentID: assignmentID, Reason: must(domain.NewReason("work abandoned: wrong approach"))}, records[len(records)-1].Event)
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
	work := []domain.Event{
		domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: must(domain.NewDescription("bug")), Branch: must(domain.NewBranch("main"))},
		domain.StageEntered{WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1, Gate: domain.GateHuman, Budget: budget3},
		abandoned,
	}

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, work...))
		require.NoError(t, s.Append(eventlog.AssignmentStream(assignmentID), 0, agent, opened))

		first := &countingLog{Log: s}
		_, err := Run(first)
		require.NoError(t, err)
		again := &countingLog{Log: s}
		_, err = Run(again)
		require.NoError(t, err)

		assert.Equal(t, 2, first.reads, "one read, one after the append")
		assert.Equal(t, 1, again.reads, "nothing to do: one read")
		return nil
	}))
}
