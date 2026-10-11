package returnworkonfixeraccepted

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
)

func work(t *testing.T, events ...domain.Event) domain.WorkState {
	t.Helper()
	var history []domain.WorkEvent
	for _, e := range events {
		if w, ok := e.(domain.WorkEvent); ok {
			history = append(history, w)
		}
	}
	w, err := domain.RebuildWork(history)
	require.NoError(t, err)
	return w
}

var (
	toImplement     = Route(domain.StageImplement, "the fix misses a case")
	fixerAccepted   = domain.AssignmentAccepted{AssignmentID: AssignmentOn(domain.StageFixer), WorkID: WorkID, Stage: domain.StageFixer, Visit: Visit1, Route: toImplement}
	backToImplement = []domain.Event{
		domain.ReworkStarted{WorkID: WorkID, Target: domain.StageImplement, Visit: Visit(2), Reason: Reason("the fix misses a case")},
		EnteredVisit(domain.StageImplement, 2),
	}
)

func TestWorkToReturn_InTheAcceptedFixerVisit_Returns(t *testing.T) {
	w, ok := workToReturn(fixerAccepted, work(t, InFixer(domain.StageReview)...))

	require.True(t, ok)
	assert.Equal(t, domain.StageFixer, w.CurrentStage())
}

func TestWorkToReturn_AlreadyReturned_DoesNothing(t *testing.T) {
	_, ok := workToReturn(fixerAccepted, work(t, append(InFixer(domain.StageReview), backToImplement...)...))

	assert.False(t, ok)
}

func TestWorkToReturn_AnotherStagesAcceptance_DoesNothing(t *testing.T) {
	_, ok := workToReturn(Accepted(domain.StageReview), work(t, InFixer(domain.StageReview)...))

	assert.False(t, ok)
}

func TestWorkToReturn_WorkNoLongerActive_DoesNothing(t *testing.T) {
	abandoned := domain.WorkAbandoned{WorkID: WorkID, Owner: Owner, Reason: Reason("x")}

	_, ok := workToReturn(fixerAccepted, work(t, append(InFixer(domain.StageReview), abandoned)...))

	assert.False(t, ok)
}

// runTwice runs the automation twice: the second run must add nothing.
func runTwice(log *eventlog.Session) error {
	if _, err := Run(log); err != nil {
		return err
	}
	_, err := Run(log)
	return err
}

func TestRun_ReturnsTheWorkToTheRoutesTargetOnceAndIsIdempotent(t *testing.T) {
	eventlogtest.Given(t, append(InFixer(domain.StageReview), fixerAccepted)...).
		When(runTwice).
		Then(backToImplement...).
		ThenRecordedBy(eventlog.AutomationActor(Name))
}
