package reject_test

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
	"github.com/jordiSalazarr/go-fast/workflow/slices/reject"
)

var owner = eventlog.OwnerActor(Owner)

func rejecting(log *eventlog.Session) error {
	_, _, err := reject.Reject(log, owner, Branch, Owner, Feedback("needs logs"))
	return err
}

func TestGivenAssignmentAwaitingApproval_WhenTheOwnerRejects_ThenItGoesBackWithFeedback(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageDiscovery, SubmittedForApproval(domain.StageDiscovery, 1))...).
		When(rejecting).
		Then(Rejected(domain.StageDiscovery, 1, "needs logs")).
		ThenRecordedBy(owner)
}

func TestGivenTheLastAttemptAwaitingApproval_WhenTheOwnerRejects_ThenTheAssignmentEscalates(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageSpecify,
		Failed(domain.StageSpecify, 1, "a"),
		SubmittedForApproval(domain.StageSpecify, 2), Rejected(domain.StageSpecify, 2, "b"),
		SubmittedForApproval(domain.StageSpecify, 3))...).
		When(rejecting).
		Then(Rejected(domain.StageSpecify, 3, "needs logs"), Escalated(domain.StageSpecify, 3, 3))
}

func TestGivenOpenAssignment_WhenTheOwnerRejects_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageDiscovery)...).
		When(rejecting).
		ThenRejected(domain.ErrNotAwaitingApproval)
}

func TestGivenAnAgent_WhenRunningReject_ThenItIsRefusedBeforeTouchingTheLog(t *testing.T) {
	agent := Must(caller.Resolve(func(string) string { return "agent" }, nil, true))
	opened := false
	cmd := reject.NewCommand(
		func() (*eventlog.Store, error) { opened = true; return nil, errors.New("must not open") },
		func() (caller.Caller, error) { return agent, nil },
		func() (domain.Branch, error) { return Branch, nil },
	)
	cmd.SetArgs([]string{"looks wrong"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := cmd.Execute()

	require.ErrorIs(t, err, caller.ErrOwnerOnly)
	assert.False(t, opened)
}
