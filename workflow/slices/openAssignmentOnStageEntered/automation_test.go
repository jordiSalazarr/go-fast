package openassignmentonstageentered

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
	entered = domain.StageEntered{WorkID: workID, Stage: domain.StageDiscovery, Visit: domain.FirstVisit(), Gate: domain.GateHuman, Budget: budget3}
)

func TestAssignmentToOpen_ForAnEnteredStage_OpensIt(t *testing.T) {
	cmd, ok := assignmentToOpen(entered, false)

	require.True(t, ok)
	assert.Equal(t, openAssignment{work: workID, stage: domain.StageDiscovery, visit: domain.FirstVisit(), gate: domain.GateHuman, budget: budget3}, cmd)
}

func TestAssignmentToOpen_AlreadyOpened_DoesNothing(t *testing.T) {
	_, ok := assignmentToOpen(entered, true)

	assert.False(t, ok)
}

func TestRun_OpensOnceAndIsIdempotent(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	started := domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: must(domain.NewDescription("bug")), Branch: must(domain.NewBranch("main"))}

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, eventlog.AgentActor("agent"), started, entered))

		var opened []domain.AssignmentID
		record := func(id domain.AssignmentID) { opened = append(opened, id) }
		first, err := Run(s, record)
		require.NoError(t, err)
		second, err := Run(s, record)
		require.NoError(t, err)

		assert.Equal(t, 1, first)
		assert.Equal(t, 0, second)
		assert.Equal(t, []domain.AssignmentID{domain.AssignmentIDFor(workID, domain.StageDiscovery, domain.FirstVisit())}, opened,
			"told once, after appending")
		records, _ := s.ReadAll()
		require.Len(t, records, 3)
		assert.Equal(t, domain.AssignmentOpened{
			AssignmentID: domain.AssignmentIDFor(workID, domain.StageDiscovery, domain.FirstVisit()),
			WorkID:       workID, Stage: domain.StageDiscovery, Visit: domain.FirstVisit(), Gate: domain.GateHuman, Budget: budget3,
		}, records[2].Event)
		assert.Equal(t, eventlog.AutomationActor(Name), records[2].Actor)
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
	started := domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: must(domain.NewDescription("bug")), Branch: must(domain.NewBranch("main"))}

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, eventlog.AgentActor("agent"), started, entered))

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
