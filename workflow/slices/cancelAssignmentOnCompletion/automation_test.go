package cancelassignmentoncompletion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

func TestRun_CancelsTheOpenVisitsOfACompletedWork_Once(t *testing.T) {
	store := Must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")
	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(WorkID), 0, agent, append(WorkOn(domain.StageImplement), domain.WorkCompleted{WorkID: WorkID})...))
		require.NoError(t, s.Append(eventlog.AssignmentStream(AssignmentOn(domain.StageImplement)), 0, agent, Opened(domain.StageImplement)))

		first, err := Run(s)
		require.NoError(t, err)
		second, err := Run(s)
		require.NoError(t, err)
		assert.Equal(t, 1, first)
		assert.Equal(t, 0, second)

		state, _, err := eventlog.NewHistory(Must(s.ReadAll())).Assignment(AssignmentOn(domain.StageImplement))
		require.NoError(t, err)
		assert.IsType(t, domain.Cancelled{}, state)
		return nil
	}))
}

func TestRun_LeavesTheVisitsOfAWorkInProgress(t *testing.T) {
	store := Must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")
	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		require.NoError(t, s.Append(eventlog.WorkStream(WorkID), 0, agent, WorkOn(domain.StageImplement)...))
		require.NoError(t, s.Append(eventlog.AssignmentStream(AssignmentOn(domain.StageImplement)), 0, agent, Opened(domain.StageImplement)))

		n, err := Run(s)
		require.NoError(t, err)
		assert.Zero(t, n)
		return nil
	}))
}
