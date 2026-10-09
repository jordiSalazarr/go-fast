package abandonwork_test

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	abandonwork "github.com/jordiSalazarr/go-fast/workflow/slices/abandonWork"
)

var owner = eventlog.OwnerActor(Owner)

func abandoning(log *eventlog.Session) error {
	_, err := abandonwork.AbandonWork(log, owner, Branch, Owner, Reason("not reproducible"))
	return err
}

// Cancelling the open assignment is the job of the cancelAssignmentOnAbandon
// automation, not of this slice.
func TestGivenActiveWork_WhenTheOwnerAbandonsIt_ThenTheWorkIsAbandoned(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageImplement, Failed(domain.StageImplement, 1, "a"))...).
		When(abandoning).
		Then(domain.WorkAbandoned{WorkID: WorkID, Owner: Owner, Reason: Reason("not reproducible")}).
		ThenRecordedBy(owner)
}

func TestGivenNoActiveWork_WhenTheOwnerAbandons_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, Completed()...).
		When(abandoning).
		ThenRejected(domain.ErrNoActiveWork)
}

func TestGivenAnAgent_WhenRunningAbandon_ThenItIsRefusedBeforeTouchingTheLog(t *testing.T) {
	agent := Must(caller.Resolve(func(string) string { return "agent" }, nil, true))
	opened := false
	cmd := abandonwork.NewCommand(
		func() (*eventlog.Store, error) { opened = true; return nil, errors.New("must not open") },
		func() (caller.Caller, error) { return agent, nil },
		func() (domain.Branch, error) { return Branch, nil },
	)
	cmd.SetArgs([]string{"give up"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()

	require.ErrorIs(t, err, caller.ErrOwnerOnly)
	assert.False(t, opened)
}
