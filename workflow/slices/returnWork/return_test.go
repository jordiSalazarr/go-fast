package returnwork_test

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	returnwork "github.com/jordiSalazarr/go-fast/workflow/slices/returnWork"
)

var owner = eventlog.OwnerActor(Owner)

func returningTo(target domain.Stage) func(*eventlog.Session) error {
	return func(log *eventlog.Session) error {
		_, err := returnwork.ReturnWork(log, owner, Branch, Owner, target, Reason("the specs miss refunds"))
		return err
	}
}

// Cancelling the current assignment is the job of the
// cancelAssignmentOnReturn automation, not of this slice.
func TestGivenWorkOnImplement_WhenTheOwnerReturnsItToSpecify_ThenSpecifyEntersItsSecondVisit(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageImplement, Failed(domain.StageImplement, 1, "a"))...).
		When(returningTo(domain.StageSpecify)).
		Then(
			domain.WorkReturned{WorkID: WorkID, Owner: Owner, From: domain.StageImplement, FromVisit: Visit1,
				Target: domain.StageSpecify, Visit: Visit(2), Reason: Reason("the specs miss refunds")},
			EnteredVisit(domain.StageSpecify, 2),
		).
		ThenRecordedBy(owner)
}

func TestGivenWorkOnSpecify_WhenTheOwnerReturnsItToALaterStage_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageSpecify)...).
		When(returningTo(domain.StageReview)).
		ThenRejected(domain.ErrInvalidReturn)
}

func TestGivenWorkInTheFixer_WhenTheOwnerReturnsIt_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, InFixer(domain.StageReview)...).
		When(returningTo(domain.StageSpecify)).
		ThenRejected(domain.ErrNotReturnable)
}

func TestGivenNoActiveWork_WhenTheOwnerReturnsIt_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, Completed()...).
		When(returningTo(domain.StageSpecify)).
		ThenRejected(domain.ErrNoActiveWork)
}

func TestGivenAnAgent_WhenRunningReturn_ThenItIsRefusedBeforeTouchingTheLog(t *testing.T) {
	agent := Must(caller.Resolve(func(string) string { return "agent" }, nil, true))
	opened := false
	cmd := returnwork.NewCommand(
		func() (*eventlog.Store, error) { opened = true; return nil, errors.New("must not open") },
		automations.Runner{},
		func() (caller.Caller, error) { return agent, nil },
		func() (domain.Branch, error) { return Branch, nil },
	)
	cmd.SetArgs([]string{"specify", "the specs miss refunds"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()

	require.ErrorIs(t, err, caller.ErrOwnerOnly)
	assert.False(t, opened)
}
