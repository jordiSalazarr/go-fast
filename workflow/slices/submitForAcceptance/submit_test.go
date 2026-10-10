package submitforacceptance_test

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog/eventlogtest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	submitforacceptance "github.com/jordiSalazarr/go-fast/workflow/slices/submitForAcceptance"
)

var agent = eventlog.AgentActor("agent")

func submitting(claim domain.AgentClaim, changed ...string) func(*eventlog.Session) error {
	return submittingChecked(claim, checksPassing, changed...)
}

func submittingChecked(claim domain.AgentClaim, checks submitforacceptance.Checks, changed ...string) func(*eventlog.Session) error {
	return func(log *eventlog.Session) error {
		_, err := submitforacceptance.SubmitForAcceptance(log, agent, Branch, claim, changes(changed...), checks)
		return err
	}
}

// checksPassing passes every check the stage requires.
func checksPassing(v exitchecks.Visit, _ io.Writer) ([]domain.CheckResult, error) {
	var results []domain.CheckResult
	for _, c := range v.Stage.RequiredChecks() {
		results = append(results, domain.CheckPassed(c))
	}
	return results, nil
}

// checksFailing fails one check and passes the rest.
func checksFailing(failing domain.ExitCheck, summary string) submitforacceptance.Checks {
	return func(v exitchecks.Visit, w io.Writer) ([]domain.CheckResult, error) {
		results, _ := checksPassing(v, w)
		for i, r := range results {
			if r.Check() == failing {
				results[i] = Must(domain.CheckFailed(failing, summary))
			}
		}
		return results, nil
	}
}

// changes reports the given files as changed since the stage visit began.
func changes(names ...string) submitforacceptance.Changes {
	return func(domain.AssignmentID) (domain.ChangedFiles, bool, error) {
		var paths []domain.RepoPath
		for _, n := range names {
			paths = append(paths, Must(domain.NewRepoPath("/repo", n)))
		}
		return domain.NewChangedFiles(paths...), false, nil
	}
}

var (
	passed = domain.ClaimPassed()
	failed = domain.ClaimFailed(Reason("tests fail"))
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

func TestGivenOpenAssignmentOnSpecify_WhenSubmittingAPassWithCodeChanged_ThenTheAttemptFails(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageSpecify)...).
		When(submitting(passed, "main.go", "main_test.go")).
		Then(Failed(domain.StageSpecify, 1, "Changed files outside the 'specify' write scope: main.go."))
}

func TestGivenOpenAssignmentOnSpecify_WhenSubmittingAPassWithOnlyTestsChanged_ThenItAwaitsApproval(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageSpecify)...).
		When(submitting(passed, "main_test.go")).
		Then(SubmittedForApproval(domain.StageSpecify, 1))
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

func TestGivenOpenAssignment_WhenAPassFailsAnExitCheck_ThenTheAttemptFailsWithItsSummary(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageSpecify)...).
		When(submittingChecked(passed, checksFailing(domain.CheckSpecsFail, "no test written in this stage fails"), "a_test.go")).
		Then(Failed(domain.StageSpecify, 1, "specs-fail: no test written in this stage fails."))
}

func TestGivenOpenAssignment_WhenSubmittingAFailedClaim_ThenNoChecksRun(t *testing.T) {
	noChecks := func(exitchecks.Visit, io.Writer) ([]domain.CheckResult, error) {
		t.Fatal("checks ran for a failed claim")
		return nil, nil
	}
	eventlogtest.Given(t, OpenOn(domain.StageImplement)...).
		When(submittingChecked(failed, noChecks)).
		Then(Failed(domain.StageImplement, 1, "tests fail"))
}

func TestGivenImplementAfterSpecify_WhenChecking_ThenTheChecksAreToldAboutBothVisits(t *testing.T) {
	var got exitchecks.Visit
	spy := func(v exitchecks.Visit, w io.Writer) ([]domain.CheckResult, error) {
		got = v
		return checksPassing(v, w)
	}
	eventlogtest.Given(t, OpenOn(domain.StageImplement, Failed(domain.StageImplement, 1, "a"))...).
		When(submittingChecked(passed, spy, "main.go")).
		Then(Accepted(domain.StageImplement))

	assert.Equal(t, AssignmentOn(domain.StageImplement), got.Assignment)
	assert.Equal(t, domain.StageImplement, got.Stage)
	assert.Equal(t, Attempt(2), got.Attempt)
	assert.Equal(t, AssignmentOn(domain.StageSpecify), got.Specify)
	assert.Equal(t, []domain.RepoPath{Must(domain.NewRepoPath("/repo", "main.go"))}, got.Changed.Paths())
}

func TestGivenAFoundAssignment_WhenItChangesBeforeSubmitting_ThenTheSubmissionConflicts(t *testing.T) {
	var target submitforacceptance.Target
	var checked submitforacceptance.Checked
	scenario := eventlogtest.Given(t, OpenOn(domain.StageImplement)...).
		When(func(log *eventlog.Session) error {
			var err error
			if target, err = submitforacceptance.Find(log, Branch); err != nil {
				return err
			}
			checked, err = submitforacceptance.Check(target, passed, changes(), checksPassing, io.Discard)
			return err
		}).
		Then()
	// While the checks ran, another submission used an attempt.
	scenario.When(submitting(failed)).Then(Failed(domain.StageImplement, 1, "tests fail"))

	scenario.When(func(log *eventlog.Session) error {
		_, err := submitforacceptance.Submit(log, agent, target, passed, checked)
		return err
	})

	require.ErrorIs(t, scenario.Err(), eventlog.ErrVersionConflict)
}
