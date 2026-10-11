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

		require.NoError(t, automations.Runner{}.Run(s))
		after, _ := s.ReadAll()
		require.NoError(t, automations.Runner{}.Run(s))
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
		err := automations.Runner{}.AroundCommand(s, func() error {
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
		return automations.Runner{}.AroundCommand(s, func() error { return boom })
	})

	require.ErrorIs(t, err, boom)
}

// A handover takes the work into the fixer and opens its assignment; the
// fixer's acceptance returns the work to a new visit of the route's target
// and opens that.
func TestRun_CarriesAHandoverThroughTheFixerAndBack(t *testing.T) {
	store := must(eventlog.Open(t.TempDir()))
	agent := eventlog.AgentActor("agent")
	workID := must(domain.NewWorkID("w1"))
	branch := must(domain.NewBranch("main"))
	reason := must(domain.NewReason("suite fails"))

	require.NoError(t, store.Exclusive(func(s *eventlog.Session) error {
		started := must(domain.StartWork(workID, domain.WorkTypeFixBug, must(domain.NewDescription("bug")), branch, must(domain.ActiveWorkOn(branch, nil))))
		require.NoError(t, s.Append(eventlog.WorkStream(workID), 0, agent, eventlog.Events(started)...))
		// Walk to review by accepting each stage.
		for range 3 {
			require.NoError(t, automations.Runner{}.Run(s))
			w := activeWork(t, s, branch)
			id := w.CurrentAssignment()
			events := []domain.Event{domain.AssignmentAccepted{AssignmentID: id, WorkID: workID, Stage: w.CurrentStage(), Visit: w.CurrentVisit()}}
			if w.CurrentStep().Gate() == domain.GateHuman {
				events = append([]domain.Event{domain.SubmittedForApproval{AssignmentID: id, Attempt: must(domain.NewAttempt(1))}}, events...)
			}
			require.NoError(t, s.Append(eventlog.AssignmentStream(id), 1, agent, events...))
		}
		require.NoError(t, automations.Runner{}.Run(s))
		review := activeWork(t, s, branch)
		require.Equal(t, domain.StageReview, review.CurrentStage())
		require.NoError(t, s.Append(eventlog.AssignmentStream(review.CurrentAssignment()), 1, agent, domain.AssignmentHandedToFixer{
			AssignmentID: review.CurrentAssignment(), WorkID: workID, Stage: domain.StageReview, Visit: domain.FirstVisit(),
			Attempt: must(domain.NewAttempt(1)), Reason: reason,
		}))

		require.NoError(t, automations.Runner{}.Run(s))

		fixer := activeWork(t, s, branch)
		assert.Equal(t, domain.StageFixer, fixer.CurrentStage())
		records, _ := s.ReadAll()
		assert.IsType(t, domain.AssignmentOpened{}, records[len(records)-1].Event, "the fixer's assignment is opened")

		route := must(domain.NewRoute(domain.StageImplement, must(domain.NewReason("the fix misses a case"))))
		require.NoError(t, s.Append(eventlog.AssignmentStream(fixer.CurrentAssignment()), 1, agent, domain.AssignmentAccepted{
			AssignmentID: fixer.CurrentAssignment(), WorkID: workID, Stage: domain.StageFixer, Visit: domain.FirstVisit(), Route: route,
		}))

		require.NoError(t, automations.Runner{}.Run(s))

		back := activeWork(t, s, branch)
		assert.Equal(t, domain.StageImplement, back.CurrentStage())
		assert.Equal(t, 2, back.CurrentVisit().Int())
		records, _ = s.ReadAll()
		last := records[len(records)-1].Event
		require.IsType(t, domain.AssignmentOpened{}, last)
		assert.Equal(t, domain.AssignmentIDFor(workID, domain.StageImplement, must(domain.NewVisit(2))), last.(domain.AssignmentOpened).AssignmentID)
		return nil
	}))
}

func activeWork(t *testing.T, s *eventlog.Session, branch domain.Branch) domain.ActiveWork {
	t.Helper()
	records, err := s.ReadAll()
	require.NoError(t, err)
	fact, err := eventlog.NewHistory(records).ActiveWorkOn(branch)
	require.NoError(t, err)
	w, err := fact.Work()
	require.NoError(t, err)
	return w
}
