package submitforacceptance_test

import (
	"io"
	"testing"
	"time"

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
		_, err := submitforacceptance.SubmitForAcceptance(log, agent, Branch, claim, changes(changed...), defaultScope, checks, submitforacceptance.Baseline{})
		return err
	}
}

// checksPassing passes every check the stage requires.
func checksPassing(v exitchecks.Visit, _ io.Writer) ([]domain.CheckResult, error) {
	var results []domain.CheckResult
	for _, c := range domain.RequiredChecks(v.Stage, v.Path) {
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
		When(submitting(domain.ClaimStressDecided(StressSkipped))).
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
	given := append(specifyAccepted(), Opened(domain.StageImplement), Failed(domain.StageImplement, 1, "a"))
	eventlogtest.Given(t, given...).
		When(submittingChecked(passed, spy, "main.go")).
		Then(Accepted(domain.StageImplement))

	assert.Equal(t, AssignmentOn(domain.StageImplement), got.Assignment)
	assert.Equal(t, domain.StageImplement, got.Stage)
	assert.Equal(t, Attempt(2), got.Attempt)
	assert.Equal(t, AssignmentOn(domain.StageSpecify), got.Specify)
	assert.Equal(t, AssignmentOn(domain.StageImplement), got.SpecifyEnd)
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
			checked, err = submitforacceptance.Check(target, passed, changes(), defaultScope, checksPassing, submitforacceptance.Baseline{}, io.Discard)
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

// checksMustNotRun fails the test if the exit checks run.
func checksMustNotRun(t *testing.T) submitforacceptance.Checks {
	return func(exitchecks.Visit, io.Writer) ([]domain.CheckResult, error) {
		t.Error("the exit checks ran")
		return nil, nil
	}
}

func TestGivenTheFixer_WhenSubmittingAPassWithAnAllowedRoute_ThenItIsAcceptedWithTheRoute(t *testing.T) {
	route := Route(domain.StageImplement, "the fix ignores negative ports")

	eventlogtest.Given(t, InFixer(domain.StageReview)...).
		When(submitting(domain.ClaimRouted(route), ".gofast/works/w1/fixer-v1.md")).
		Then(domain.AssignmentAccepted{AssignmentID: AssignmentOn(domain.StageFixer), WorkID: WorkID, Stage: domain.StageFixer, Visit: Visit1, Route: route})
}

func TestGivenTheFixer_WhenSubmittingARouteThatIsNotAllowed_ThenItIsRejectedBeforeAnyCheckRuns(t *testing.T) {
	s := eventlogtest.Given(t, InFixer(domain.StageReview)...).
		When(submittingChecked(domain.ClaimRouted(Route(domain.StageReview, "review again")), checksMustNotRun(t))).
		ThenRejected(domain.ErrInvalidRoute)

	var route *domain.InvalidRouteError
	require.ErrorAs(t, s.Err(), &route)
	assert.Equal(t, []domain.Stage{domain.StageSpecify, domain.StageImplement}, route.Allowed)
}

func TestGivenTheFixer_WhenSubmittingAPassWithoutARoute_ThenItIsRejectedNamingTheTargets(t *testing.T) {
	s := eventlogtest.Given(t, InFixer(domain.StageReview)...).
		When(submittingChecked(passed, checksMustNotRun(t))).
		ThenRejected(domain.ErrMissingRoute)

	var missing *domain.RouteMissingError
	require.ErrorAs(t, s.Err(), &missing)
	assert.Equal(t, []domain.Stage{domain.StageSpecify, domain.StageImplement}, missing.Allowed)
}

func TestGivenTheFixer_WhenSubmittingAFailedClaim_ThenAnAttemptFails(t *testing.T) {
	eventlogtest.Given(t, InFixer(domain.StageReview)...).
		When(submitting(failed)).
		Then(Failed(domain.StageFixer, 1, "tests fail"))
}

func TestGivenAnotherStage_WhenSubmittingARoute_ThenItIsRejectedBeforeAnyCheckRuns(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageReview)...).
		When(submittingChecked(domain.ClaimRouted(Route(domain.StageImplement, "x")), checksMustNotRun(t))).
		ThenRejected(domain.ErrRouteOutsideFixer)
}

func TestGivenReview_WhenTheSuiteFails_ThenTheWorkIsHandedToTheFixer(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageReview)...).
		When(submittingChecked(domain.ClaimStressDecided(StressSkipped), checksFailing(domain.CheckSuitePasses, "1 test failed"))).
		Then(HandedToFixer(domain.StageReview, 1, "suite-passes: 1 test failed."))
}

// specifyAccepted is the work on implement's first visit, after the owner
// approved specify's first visit.
func specifyAccepted() []domain.Event {
	return append(OpenOn(domain.StageSpecify, SubmittedForApproval(domain.StageSpecify, 1), Accepted(domain.StageSpecify)),
		StageEntered(domain.StageImplement))
}

// visitSpy records the visit the checks run for.
func visitSpy(got *exitchecks.Visit) submitforacceptance.Checks {
	return func(v exitchecks.Visit, w io.Writer) ([]domain.CheckResult, error) {
		*got = v
		return checksPassing(v, w)
	}
}

// reviewHandedBackTo is the work after review's first visit handed it to
// the fixer, which returned it to target's second visit.
func reviewHandedBackTo(target domain.Stage) []domain.Event {
	h := append(specifyAccepted(), Opened(domain.StageImplement), Accepted(domain.StageImplement), StageEntered(domain.StageReview),
		Opened(domain.StageReview), HandedToFixer(domain.StageReview, 1, "suite fails"))
	h = append(h, FixerEntered(domain.StageReview, "suite fails")...)
	return append(h,
		domain.ReworkStarted{WorkID: WorkID, Target: target, Visit: Visit(2), Reason: Reason("the fix misses a case")},
		EnteredVisit(target, 2),
		domain.AssignmentOpened{AssignmentID: domain.AssignmentIDFor(WorkID, target, Visit(2)), WorkID: WorkID, Stage: target, Visit: Visit(2),
			Gate: EnteredVisit(target, 2).Gate, Budget: EnteredVisit(target, 2).Budget})
}

func TestGivenAReworkBackToImplement_WhenChecking_ThenTheSpecsAreStillThoseOfTheFirstSpecifyVisit(t *testing.T) {
	var got exitchecks.Visit

	eventlogtest.Given(t, reviewHandedBackTo(domain.StageImplement)...).
		When(submittingChecked(passed, visitSpy(&got))).
		Then(domain.AssignmentAccepted{AssignmentID: domain.AssignmentIDFor(WorkID, domain.StageImplement, Visit(2)), WorkID: WorkID,
			Stage: domain.StageImplement, Visit: Visit(2)})

	assert.Equal(t, AssignmentOn(domain.StageSpecify), got.Specify)
	assert.Equal(t, AssignmentOn(domain.StageImplement), got.SpecifyEnd, "specify v1 ended when implement v1 began")
}

func TestGivenAReworkBackToSpecify_WhenImplementIsChecked_ThenTheSpecsAreThoseOfTheNewSpecifyVisit(t *testing.T) {
	specify2 := domain.AssignmentIDFor(WorkID, domain.StageSpecify, Visit(2))
	implement2 := domain.AssignmentIDFor(WorkID, domain.StageImplement, Visit(2))
	given := append(reviewHandedBackTo(domain.StageSpecify),
		domain.SubmittedForApproval{AssignmentID: specify2, Attempt: Attempt(1)},
		domain.AssignmentAccepted{AssignmentID: specify2, WorkID: WorkID, Stage: domain.StageSpecify, Visit: Visit(2)},
		EnteredVisit(domain.StageImplement, 2),
		domain.AssignmentOpened{AssignmentID: implement2, WorkID: WorkID, Stage: domain.StageImplement, Visit: Visit(2), Gate: domain.GateAuto, Budget: Budget(3)})
	var got exitchecks.Visit

	eventlogtest.Given(t, given...).
		When(submittingChecked(passed, visitSpy(&got))).
		Then(domain.AssignmentAccepted{AssignmentID: implement2, WorkID: WorkID, Stage: domain.StageImplement, Visit: Visit(2)})

	assert.Equal(t, specify2, got.Specify)
	assert.Equal(t, implement2, got.SpecifyEnd)
}

// walletNFR covers server/wallet/.
var walletNFR = Must(domain.NewNFR(Must(domain.NewNFRID("wallet-latency")), "", []domain.AreaGlob{Must(domain.NewAreaGlob("server/wallet/**"))},
	Must(domain.NewOperation("POST /wallets/{id}/transactions")), Must(domain.NewLoad(5, time.Second)),
	Must(domain.NewTargets(domain.TargetValues{MinRPS: ptr(10.0)}))))

func ptr[T any](v T) *T { return &v }

// workChanged reports the NFRs and the files the work changed.
func workChanged(names ...string) submitforacceptance.StressFacts {
	return func(domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error) {
		changed, _, _ := changes(names...)(domain.AssignmentID{})
		return []domain.NFR{walletNFR}, changed, nil
	}
}

// decidingStress is review submitting a pass with its stress decision, as
// the command does: the decision is checked against the NFRs first.
func decidingStress(decision domain.StressDecision, facts submitforacceptance.StressFacts, checks submitforacceptance.Checks) func(*eventlog.Session) error {
	return func(log *eventlog.Session) error {
		target, err := submitforacceptance.Find(log, Branch)
		if err != nil {
			return err
		}
		claim, err := submitforacceptance.DecideStress(target, decision, Reason("the reason"), facts)
		if err != nil {
			return err
		}
		if err := submitforacceptance.CheckClaim(target, claim); err != nil {
			return err
		}
		checked, err := submitforacceptance.Check(target, claim, changes(), defaultScope, checks, submitforacceptance.Baseline{}, io.Discard)
		if err != nil {
			return err
		}
		_, err = submitforacceptance.Submit(log, agent, target, claim, checked)
		return err
	}
}

func TestGivenReview_WhenSubmittingAPassWithoutAStressDecision_ThenItIsRejectedBeforeAnyCheckRuns(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageReview)...).
		When(submittingChecked(passed, checksMustNotRun(t))).
		ThenRejected(domain.ErrMissingStressDecision)
}

func TestGivenReviewOfAChangeOutsideEveryNFR_WhenSkippingStressTesting_ThenItIsAccepted(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageReview)...).
		When(decidingStress(domain.StressSkip, workChanged("server/server.go"), checksPassing)).
		Then(domain.AssignmentAccepted{AssignmentID: AssignmentOn(domain.StageReview), WorkID: WorkID, Stage: domain.StageReview, Visit: Visit1,
			Stress: Must(domain.NewStressChoice(domain.StressSkip, Reason("the reason"), nil))})
}

func TestGivenReviewOfAChangeInAnNFRsArea_WhenSkippingStressTesting_ThenItIsRejectedBeforeAnyCheckRuns(t *testing.T) {
	s := eventlogtest.Given(t, OpenOn(domain.StageReview)...).
		When(decidingStress(domain.StressSkip, workChanged("server/wallet/post.go"), checksMustNotRun(t))).
		ThenRejected(domain.ErrStressForced)
	var forced *domain.StressForcedError
	require.ErrorAs(t, s.Err(), &forced)
	assert.Equal(t, "wallet-latency (server/wallet/**)", forced.Describe())
}

func TestGivenReviewOfAChangeInAnNFRsArea_WhenIncludingStressTesting_ThenTheDecisionIsForcedByTheNFR(t *testing.T) {
	forced := Must(domain.NewStressChoice(domain.StressInclude, Reason("the reason"), []domain.NFRID{walletNFR.ID()}))
	eventlogtest.Given(t, OpenOn(domain.StageReview)...).
		When(decidingStress(domain.StressInclude, workChanged("server/wallet/post.go"), checksPassing)).
		Then(domain.AssignmentAccepted{AssignmentID: AssignmentOn(domain.StageReview), WorkID: WorkID, Stage: domain.StageReview, Visit: Visit1, Stress: forced})
}

func TestGivenAnotherStage_WhenDecidingStressTesting_ThenItIsRejected(t *testing.T) {
	eventlogtest.Given(t, OpenOn(domain.StageImplement)...).
		When(decidingStress(domain.StressInclude, workChanged(), checksMustNotRun(t))).
		ThenRejected(domain.ErrStressOutsideReview)
}

func defaultScope(submitforacceptance.Target) (domain.ScopeFacts, error) {
	return domain.ScopeFacts{Decisions: domain.DefaultDecisionsDir()}, nil
}
