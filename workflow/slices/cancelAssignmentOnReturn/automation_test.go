package cancelassignmentonreturn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

var returnedFromImplement = domain.WorkReturned{WorkID: WorkID, Owner: Owner, From: domain.StageImplement, FromVisit: Visit1,
	Target: domain.StageSpecify, Visit: Visit(2), Reason: Reason("the specs miss refunds")}

func assignment(t *testing.T, events ...domain.AssignmentEvent) domain.AssignmentState {
	t.Helper()
	a, err := domain.RebuildAssignment(events)
	require.NoError(t, err)
	return a
}

func TestAssignmentToCancel_OpenAssignment_IsCancelled(t *testing.T) {
	c, reason, ok := assignmentToCancel(returnedFromImplement, assignment(t, Opened(domain.StageImplement)))

	require.True(t, ok)
	assert.IsType(t, domain.Open{}, c)
	assert.Equal(t, "work returned to specify: the specs miss refunds", reason.String())
}

func TestAssignmentToCancel_AlreadyCancelledOrAccepted_DoesNothing(t *testing.T) {
	cancelled := domain.AssignmentCancelled{AssignmentID: AssignmentOn(domain.StageImplement), Reason: Reason("x")}

	_, _, ok := assignmentToCancel(returnedFromImplement, assignment(t, Opened(domain.StageImplement), cancelled))
	assert.False(t, ok)
	_, _, ok = assignmentToCancel(returnedFromImplement, assignment(t, Opened(domain.StageImplement), Accepted(domain.StageImplement)))
	assert.False(t, ok)
}

func TestRun_CancelsTheVisitReturnedFrom_OnceAndOnlyIt(t *testing.T) {
	store := Must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")
	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		work := append(WorkOn(domain.StageImplement), returnedFromImplement, EnteredVisit(domain.StageSpecify, 2))
		require.NoError(t, s.Append(eventlog.WorkStream(WorkID), 0, agent, work...))
		require.NoError(t, s.Append(eventlog.AssignmentStream(AssignmentOn(domain.StageImplement)), 0, agent, Opened(domain.StageImplement)))
		require.NoError(t, s.Append(eventlog.AssignmentStream(domain.AssignmentIDFor(WorkID, domain.StageSpecify, Visit(2))), 0, agent, OpenedVisit(domain.StageSpecify, 2)))

		first, err := Run(s)
		require.NoError(t, err)
		second, err := Run(s)
		require.NoError(t, err)
		assert.Equal(t, 1, first)
		assert.Equal(t, 0, second)

		history := eventlog.NewHistory(Must(s.ReadAll()))
		implement, _, err := history.Assignment(AssignmentOn(domain.StageImplement))
		require.NoError(t, err)
		assert.IsType(t, domain.Cancelled{}, implement)
		specify, _, err := history.Assignment(domain.AssignmentIDFor(WorkID, domain.StageSpecify, Visit(2)))
		require.NoError(t, err)
		assert.IsType(t, domain.Open{}, specify)
		return nil
	}))
}

func TestRun_AVisitNotOpenedYet_IsLeftForLater(t *testing.T) {
	store := Must(eventlog.Open(t.TempDir()))
	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		work := append(WorkOn(domain.StageImplement), returnedFromImplement, EnteredVisit(domain.StageSpecify, 2))
		require.NoError(t, s.Append(eventlog.WorkStream(WorkID), 0, eventlog.AgentActor("agent"), work...))
		n, err := Run(s)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		return nil
	}))
}
