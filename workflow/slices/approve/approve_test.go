package approve_test

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
	"github.com/jordiSalazarr/go-fast/workflow/slices/approve"
)

var owner = eventlog.OwnerActor(Owner)

func approving(log *eventlog.Session) error {
	_, err := approve.Approve(log, owner, Branch, Owner)
	return err
}

func TestGivenAssignmentAwaitingApproval_WhenTheOwnerApproves_ThenItIsAccepted(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageDiscovery, SubmittedForApproval(domain.StageDiscovery, 1))...).
		When(approving).
		Then(Accepted(domain.StageDiscovery)).
		ThenRecordedBy(owner)
}

func TestGivenOpenAssignment_WhenTheOwnerApproves_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageDiscovery)...).
		When(approving).
		ThenRejected(domain.ErrNotAwaitingApproval)
}

func TestGivenNoActiveWork_WhenTheOwnerApproves_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t).
		When(approving).
		ThenRejected(domain.ErrNoActiveWork)
}

func TestGivenAnAgent_WhenRunningApprove_ThenItIsRefusedBeforeTouchingTheLog(t *testing.T) {
	agent := Must(caller.Resolve(func(string) string { return "agent" }, nil, true))
	opened := false
	cmd := approve.NewCommand(
		func() (*eventlog.Store, error) { opened = true; return nil, errors.New("must not open") },
		func() (caller.Caller, error) { return agent, nil },
		func() (domain.Branch, error) { return Branch, nil },
	)
	cmd.SetArgs(nil)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()

	var ownerOnly *caller.OwnerOnlyError
	require.ErrorAs(t, err, &ownerOnly)
	assert.Equal(t, "gf approve", ownerOnly.Command)
	assert.False(t, opened)
}
