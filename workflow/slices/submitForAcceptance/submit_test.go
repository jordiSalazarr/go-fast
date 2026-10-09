package submitforacceptance_test

import (
	"testing"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	submitforacceptance "github.com/jordiSalazarr/go-fast/workflow/slices/submitForAcceptance"
)

var agent = eventlog.AgentActor("agent")

func submitting(outcome domain.ExitCheckOutcome) func(*eventlog.Session) error {
	return func(log *eventlog.Session) error {
		_, _, err := submitforacceptance.SubmitForAcceptance(log, agent, outcome)
		return err
	}
}

var (
	passed = domain.ExitCheckPassed()
	failed = domain.ExitCheckFailed(Reason("tests fail"))
)

func TestGivenOpenAssignment_WhenSubmittingAFailedCheck_ThenAnAttemptFails(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageImplement)...).
		When(submitting(failed)).
		Then(Failed(domain.StageImplement, 1, "tests fail")).
		ThenRecordedBy(agent)
}

func TestGivenTwoFailedAttempts_WhenSubmittingAFailedCheck_ThenTheAssignmentEscalates(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageImplement,
		Failed(domain.StageImplement, 1, "a"), Failed(domain.StageImplement, 2, "b"))...).
		When(submitting(failed)).
		Then(Failed(domain.StageImplement, 3, "tests fail"), Escalated(domain.StageImplement, 3, 3))
}

func TestGivenOpenAssignmentOnAutoGate_WhenSubmittingAPassingCheck_ThenItIsAccepted(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageReview)...).
		When(submitting(passed)).
		Then(Accepted(domain.StageReview))
}

func TestGivenOpenAssignmentOnHumanGate_WhenSubmittingAPassingCheck_ThenItAwaitsApproval(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageSpecify, Failed(domain.StageSpecify, 1, "a"))...).
		When(submitting(passed)).
		Then(SubmittedForApproval(domain.StageSpecify, 2))
}

func TestGivenAssignmentAwaitingApproval_WhenSubmitting_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageDiscovery, SubmittedForApproval(domain.StageDiscovery, 1))...).
		When(submitting(passed)).
		ThenRejected(domain.ErrNotAwaitingSubmission)
}

func TestGivenEscalatedAssignment_WhenSubmitting_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, EscalatedOn(domain.StageImplement)...).
		When(submitting(passed)).
		ThenRejected(domain.ErrNotAwaitingSubmission)
}

func TestGivenNoActiveWork_WhenSubmitting_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, Completed()...).
		When(submitting(passed)).
		ThenRejected(domain.ErrNoActiveWork)
}
