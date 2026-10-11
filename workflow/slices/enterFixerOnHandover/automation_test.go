package enterfixeronhandover

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

var handover = HandedToFixer(domain.StageReview, 1, "suite fails")

func TestWorkToHandOver_OnTheStageVisitThatHandedOver_EntersTheFixer(t *testing.T) {
	w, ok := workToHandOver(handover, work(t, WorkOn(domain.StageReview)...))

	require.True(t, ok)
	assert.Equal(t, domain.StageReview, w.CurrentStage())
}

func TestWorkToHandOver_AlreadyInTheFixer_DoesNothing(t *testing.T) {
	_, ok := workToHandOver(handover, work(t, append(WorkOn(domain.StageReview), FixerEntered(domain.StageReview, "suite fails")...)...))

	assert.False(t, ok)
}

func TestWorkToHandOver_AnEarlierVisitsHandover_DoesNothing(t *testing.T) {
	_, ok := workToHandOver(handover, work(t, ReworkedOnReview(1)...))

	assert.False(t, ok)
}

func TestWorkToHandOver_AtTheReworkLimit_WaitsForTheOwner(t *testing.T) {
	limit := domain.ReworkLimitReached{WorkID: WorkID, Reworks: Must(domain.NewReworkCount(3))}

	_, ok := workToHandOver(HandedToFixerOnVisit(domain.StageReview, 4, "x"), work(t, append(ReworkedOnReview(3), limit)...))

	assert.False(t, ok)
}

func TestWorkToHandOver_WorkNoLongerActive_DoesNothing(t *testing.T) {
	abandoned := domain.WorkAbandoned{WorkID: WorkID, Owner: Owner, Reason: Reason("x")}

	_, ok := workToHandOver(handover, work(t, append(WorkOn(domain.StageReview), abandoned)...))

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

func TestRun_EntersTheFixerOnceAndIsIdempotent(t *testing.T) {
	eventlogtest.Given(t, HandedOverOn(domain.StageReview, "suite fails")...).
		When(runTwice).
		Then(FixerEntered(domain.StageReview, "suite fails")...).
		ThenRecordedBy(eventlog.AutomationActor(Name))
}

func TestRun_AfterTheWholeReworkBudget_ReachesTheReworkLimitOnce(t *testing.T) {
	given := append(ReworkedOnReview(3), HandedToFixerOnVisit(domain.StageReview, 4, "suite fails"))

	eventlogtest.Given(t, given...).
		When(runTwice).
		Then(domain.ReworkLimitReached{WorkID: WorkID, Reworks: Must(domain.NewReworkCount(3))})
}

func TestRun_OnceTheOwnerAllowsMoreRework_ThePendingHandoverEntersTheFixer(t *testing.T) {
	given := append(ReworkedOnReview(3),
		domain.ReworkLimitReached{WorkID: WorkID, Reworks: Must(domain.NewReworkCount(3))},
		domain.ReworkBudgetExtended{WorkID: WorkID, Owner: Owner, Additional: Must(domain.NewReworkBudget(1)), NewBudget: Must(domain.NewReworkBudget(4))},
		HandedToFixerOnVisit(domain.StageReview, 4, "suite fails"))

	eventlogtest.Given(t, given...).
		When(runTwice).
		Then(
			domain.FixerEntered{WorkID: WorkID, From: domain.StageReview, FromVisit: Visit(4), Visit: Visit(4), Mode: domain.FixerModeCorrectness, Reason: Reason("suite fails")},
			EnteredVisit(domain.StageFixer, 4))
}
