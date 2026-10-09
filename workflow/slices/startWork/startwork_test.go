package startwork_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	startwork "github.com/jordiSalazarr/go-fast/workflow/slices/startWork"
)

var (
	newWorkID = Must(domain.NewWorkID("w2"))
	newDesc   = Must(domain.NewDescription("crash on save"))
	owner     = eventlog.OwnerActor(Owner)
)

func startWork(log *eventlog.Session) error {
	_, err := startwork.StartWork(log, owner, newWorkID, domain.WorkTypeFixBug, newDesc)
	return err
}

func started() []domain.Event {
	return []domain.Event{
		domain.WorkStarted{WorkID: newWorkID, WorkType: domain.WorkTypeFixBug, Description: newDesc},
		domain.StageEntered{WorkID: newWorkID, Stage: domain.StageDiscovery, Visit: Visit1, Gate: domain.GateHuman, Budget: Budget(3)},
	}
}

func TestGivenNoWork_WhenStartingWork_ThenItStartsOnDiscovery(t *testing.T) {
	eventlogtest.Given(t).
		When(startWork).
		Then(started()...).
		ThenRecordedBy(owner)
}

func TestGivenActiveWork_WhenStartingWork_ThenItIsRejectedNamingTheActiveWork(t *testing.T) {
	s := eventlogtest.Given(t, OpenOn(domain.StageSpecify)...).
		When(startWork).
		ThenRejected(domain.ErrWorkAlreadyActive)

	var active *domain.WorkAlreadyActiveError
	require.ErrorAs(t, s.Err(), &active)
	assert.Equal(t, WorkID, active.ID)
	assert.Equal(t, Description, active.Description)
}

func TestGivenCompletedWork_WhenStartingWork_ThenANewWorkStarts(t *testing.T) {
	eventlogtest.Given(t, Completed()...).
		When(startWork).
		Then(started()...)
}

func TestGivenAbandonedWork_WhenStartingWork_ThenANewWorkStarts(t *testing.T) {
	abandoned := domain.WorkAbandoned{WorkID: WorkID, Owner: Owner, Reason: Reason("wrong bug")}

	eventlogtest.Given(t, append(WorkOn(domain.StageDiscovery), abandoned)...).
		When(startWork).
		Then(started()...)
}
