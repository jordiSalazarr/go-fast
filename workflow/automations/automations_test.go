package automations_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func eventTypes(records []eventlog.Recorded) []string {
	var types []string
	for _, r := range records {
		switch r.Event.(type) {
		case domain.WorkStarted:
			types = append(types, "WorkStarted")
		case domain.StageEntered:
			types = append(types, "StageEntered")
		case domain.WorkCompleted:
			types = append(types, "WorkCompleted")
		case domain.AssignmentOpened:
			types = append(types, "AssignmentOpened")
		case domain.AssignmentAccepted:
			types = append(types, "AssignmentAccepted")
		default:
			types = append(types, "other")
		}
	}
	return types
}

// A crash after an auto-gate acceptance left the work on the old stage. The
// next run reconciles: it advances the work and opens the next assignment,
// and running again adds nothing.
func TestRun_ReconcilesAHalfFinishedCycle(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")
	workID := must(domain.NewWorkID("w1"))
	budget := must(domain.NewAttemptBudget(3))
	visit := domain.FirstVisit()
	reviewID := domain.AssignmentIDFor(workID, domain.StageReview, visit)

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		work := []domain.Event{domain.WorkStarted{WorkID: workID, WorkType: domain.WorkTypeFixBug, Description: must(domain.NewDescription("bug")), Branch: must(domain.NewBranch("main"))}}
		for _, st := range []struct {
			stage domain.Stage
			gate  domain.Gate
		}{{domain.StageDiscovery, domain.GateHuman}, {domain.StageSpecify, domain.GateHuman}, {domain.StageImplement, domain.GateAuto}, {domain.StageReview, domain.GateAuto}} {
			work = append(work, domain.StageEntered{WorkID: workID, Stage: st.stage, Visit: visit, Gate: st.gate, Budget: budget})
		}
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, work...))
		require.NoError(t, s.Append(eventlog.AssignmentStream(reviewID), 0, agent,
			domain.AssignmentOpened{AssignmentID: reviewID, WorkID: workID, Stage: domain.StageReview, Visit: visit, Gate: domain.GateAuto, Budget: budget},
			domain.AssignmentAccepted{AssignmentID: reviewID, WorkID: workID, Stage: domain.StageReview, Visit: visit},
		))
		before, _ := s.ReadAll()

		require.NoError(t, automations.Run(s))
		after, _ := s.ReadAll()
		require.NoError(t, automations.Run(s))
		again, _ := s.ReadAll()

		// Discovery..implement assignments were never opened in this crafted log,
		// so they are opened too; review's acceptance advances the work.
		added := eventTypes(after[len(before):])
		assert.Contains(t, added, "StageEntered")
		assert.Equal(t, 4, countOf(added, "AssignmentOpened"), "discovery, specify, implement, integration-testing")
		last := after[len(after)-1].Event
		assert.IsType(t, domain.AssignmentOpened{}, last)
		assert.Equal(t, domain.StageIntegrationTesting, last.(domain.AssignmentOpened).Stage)
		assert.Len(t, again, len(after), "second run is a no-op")
		return nil
	}))
}

func countOf(values []string, v string) int {
	n := 0
	for _, x := range values {
		if x == v {
			n++
		}
	}
	return n
}

func TestAroundCommand_RunsAutomationsBeforeAndAfter(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	workID := must(domain.NewWorkID("w1"))
	agent := eventlog.AgentActor("agent")

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		branch := must(domain.NewBranch("main"))
		events := must(domain.StartWork(workID, domain.WorkTypeFixBug, must(domain.NewDescription("bug")), branch, must(domain.ActiveWorkOn(branch, nil))))
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, eventlog.Events(events)...))

		var seenBeforeCommand []eventlog.Recorded
		err := automations.AroundCommand(s, func() error {
			seenBeforeCommand, _ = s.ReadAll()
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"WorkStarted", "StageEntered", "AssignmentOpened"}, eventTypes(seenBeforeCommand))
		return nil
	}))
}

func TestAroundCommand_ReturnsTheCommandError(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	boom := errors.New("boom")

	err := store.Exclusive(func(s *eventlog.Session) error {
		return automations.AroundCommand(s, func() error { return boom })
	})

	require.ErrorIs(t, err, boom)
}
