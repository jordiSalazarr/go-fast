package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

var (
	humanAssignmentID = domain.AssignmentIDFor(workID, domain.StageDiscovery, visit1)
	autoAssignmentID  = domain.AssignmentIDFor(workID, domain.StageImplement, visit1)
)

func openedOnHumanGate() []domain.AssignmentEvent {
	return []domain.AssignmentEvent{domain.AssignmentOpened{
		AssignmentID: humanAssignmentID, WorkID: workID, Stage: domain.StageDiscovery,
		Visit: visit1, Gate: domain.GateHuman, Budget: budget3,
	}}
}

func openedOnAutoGate() []domain.AssignmentEvent {
	return []domain.AssignmentEvent{domain.AssignmentOpened{
		AssignmentID: autoAssignmentID, WorkID: workID, Stage: domain.StageImplement,
		Visit: visit1, Gate: domain.GateAuto, Budget: budget3,
	}}
}

func failed(id domain.AssignmentID, n int) domain.AssignmentEvent {
	return domain.AttemptFailed{AssignmentID: id, Attempt: attempt(n), Reason: reason}
}

func awaitingApproval(n int) domain.AssignmentEvent {
	return domain.SubmittedForApproval{AssignmentID: humanAssignmentID, Attempt: attempt(n)}
}

func rejected(n int) domain.AssignmentEvent {
	return domain.AssignmentRejected{AssignmentID: humanAssignmentID, Owner: owner, Feedback: feedback, Attempt: attempt(n)}
}

func escalated(id domain.AssignmentID, used, b int) domain.AssignmentEvent {
	return domain.AssignmentEscalated{AssignmentID: id, AttemptsUsed: attempts(used), Budget: budget(b)}
}

func history(parts ...[]domain.AssignmentEvent) []domain.AssignmentEvent {
	var all []domain.AssignmentEvent
	for _, p := range parts {
		all = append(all, p...)
	}
	return all
}

func events(e ...domain.AssignmentEvent) []domain.AssignmentEvent { return e }

var noChanges = domain.ChangedFiles{}

func TestAssignmentIDFor_IsDeterministic(t *testing.T) {
	a := domain.AssignmentIDFor(workID, domain.StageReview, visit1)
	b := domain.AssignmentIDFor(workID, domain.StageReview, visit1)

	assert.Equal(t, a, b)
	assert.NotEqual(t, a, domain.AssignmentIDFor(workID, domain.StageImplement, visit1))
	assert.NotEqual(t, a, domain.AssignmentIDFor(otherWorkID, domain.StageReview, visit1))
	assert.NotEqual(t, a, domain.AssignmentIDFor(workID, domain.StageReview, must(domain.NewVisit(2))))
	assert.Equal(t, a, must(domain.ParseAssignmentID(a.String())))
}

func TestOpenAssignment(t *testing.T) {
	got, err := domain.OpenAssignment(workID, domain.StageDiscovery, visit1, domain.GateHuman, budget3)

	thenEvents(t, got, err, openedOnHumanGate()...)
	open := stateIs[domain.Open](t, thenAssignmentState(t, nil, got))
	assert.Equal(t, attempts(0), open.AttemptsUsed())
	assert.Equal(t, budget3, open.Budget())
}

func TestOpenAssignment_WithMissingValue_IsRejected(t *testing.T) {
	got, err := domain.OpenAssignment(workID, domain.StageDiscovery, visit1, domain.Gate{}, budget3)

	thenRejected(t, got, err, domain.ErrMissingValue)
}

func TestSubmitForAcceptance_Failed_UsesAnAttempt(t *testing.T) {
	open := stateIs[domain.Open](t, givenAssignment(t, openedOnAutoGate()...))

	got, err := open.SubmitForAcceptance(domain.ClaimFailed(reason), noChanges)

	thenEvents(t, got, err, failed(autoAssignmentID, 1))
	after := stateIs[domain.Open](t, thenAssignmentState(t, openedOnAutoGate(), got))
	assert.Equal(t, attempts(1), after.AttemptsUsed())
}

func TestSubmitForAcceptance_FailedReachingTheBudget_Escalates(t *testing.T) {
	given := history(openedOnAutoGate(), events(failed(autoAssignmentID, 1), failed(autoAssignmentID, 2)))
	open := stateIs[domain.Open](t, givenAssignment(t, given...))

	got, err := open.SubmitForAcceptance(domain.ClaimFailed(reason), noChanges)

	thenEvents(t, got, err, failed(autoAssignmentID, 3), escalated(autoAssignmentID, 3, 3))
	stateIs[domain.Escalated](t, thenAssignmentState(t, given, got))
}

func TestSubmitForAcceptance_WithoutClaim_IsRejected(t *testing.T) {
	open := stateIs[domain.Open](t, givenAssignment(t, openedOnAutoGate()...))

	got, err := open.SubmitForAcceptance(domain.AgentClaim{}, noChanges)

	thenRejected(t, got, err, domain.ErrMissingValue)
}

func TestSubmitForAcceptance_PassedOnAutoGate_IsAccepted(t *testing.T) {
	given := history(openedOnAutoGate(), events(failed(autoAssignmentID, 1)))
	open := stateIs[domain.Open](t, givenAssignment(t, given...))

	got, err := open.SubmitForAcceptance(domain.ClaimPassed(), noChanges)

	thenEvents(t, got, err, domain.AssignmentEvent(domain.AssignmentAccepted{
		AssignmentID: autoAssignmentID, WorkID: workID, Stage: domain.StageImplement, Visit: visit1,
	}))
	stateIs[domain.Accepted](t, thenAssignmentState(t, given, got))
}

func TestSubmitForAcceptance_PassedOnHumanGate_AwaitsApproval(t *testing.T) {
	given := history(openedOnHumanGate(), events(failed(humanAssignmentID, 1)))
	open := stateIs[domain.Open](t, givenAssignment(t, given...))

	got, err := open.SubmitForAcceptance(domain.ClaimPassed(), noChanges)

	thenEvents(t, got, err, awaitingApproval(2))
	waiting := stateIs[domain.AwaitingApproval](t, thenAssignmentState(t, given, got))
	assert.Equal(t, attempt(2), waiting.Attempt())
}

func TestSubmitForAcceptance_PassedWithChangesOutsideTheWriteScope_FailsTheAttempt(t *testing.T) {
	open := stateIs[domain.Open](t, givenAssignment(t, openedOnHumanGate()...))
	changed := domain.NewChangedFiles(
		repoPath(t, "main.go"), repoPath(t, ".gofast/works/w1/discovery-v1.md"), repoPath(t, "internal/x.go"))

	got, err := open.SubmitForAcceptance(domain.ClaimPassed(), changed)

	thenEvents(t, got, err, domain.AssignmentEvent(domain.AttemptFailed{
		AssignmentID: humanAssignmentID, Attempt: attempt(1),
		Reason: reasonOf("Changed files outside the 'discovery' write scope: internal/x.go, main.go."),
	}))
	after := stateIs[domain.Open](t, thenAssignmentState(t, openedOnHumanGate(), got))
	assert.Equal(t, attempts(1), after.AttemptsUsed())
}

func TestSubmitForAcceptance_PassedWithChangesOutsideTheWriteScope_CanEscalate(t *testing.T) {
	given := history(openedOnHumanGate(), events(failed(humanAssignmentID, 1), failed(humanAssignmentID, 2)))
	open := stateIs[domain.Open](t, givenAssignment(t, given...))

	got, err := open.SubmitForAcceptance(domain.ClaimPassed(), domain.NewChangedFiles(repoPath(t, "main.go")))

	thenEvents(t, got, err,
		domain.AssignmentEvent(domain.AttemptFailed{
			AssignmentID: humanAssignmentID, Attempt: attempt(3),
			Reason: reasonOf("Changed files outside the 'discovery' write scope: main.go."),
		}),
		escalated(humanAssignmentID, 3, 3))
	stateIs[domain.Escalated](t, thenAssignmentState(t, given, got))
}

func TestSubmitForAcceptance_FailedWithChangesOutsideTheWriteScope_GivesBothReasons(t *testing.T) {
	open := stateIs[domain.Open](t, givenAssignment(t, openedOnHumanGate()...))

	got, err := open.SubmitForAcceptance(domain.ClaimFailed(reasonOf("cannot reproduce")), domain.NewChangedFiles(repoPath(t, "main.go")))

	thenEvents(t, got, err, domain.AssignmentEvent(domain.AttemptFailed{
		AssignmentID: humanAssignmentID, Attempt: attempt(1),
		Reason: reasonOf("cannot reproduce. Changed files outside the 'discovery' write scope: main.go."),
	}))
}

func TestSubmitForAcceptance_PassedWithChangesInsideTheWriteScope_Passes(t *testing.T) {
	open := stateIs[domain.Open](t, givenAssignment(t, openedOnHumanGate()...))

	got, err := open.SubmitForAcceptance(domain.ClaimPassed(), domain.NewChangedFiles(repoPath(t, ".gofast/works/w1/discovery-v1.md")))

	thenEvents(t, got, err, awaitingApproval(1))
}

func TestApprove_AcceptsTheAssignment(t *testing.T) {
	given := history(openedOnHumanGate(), events(awaitingApproval(1)))
	waiting := stateIs[domain.AwaitingApproval](t, givenAssignment(t, given...))

	got, err := waiting.Approve(owner)

	thenEvents(t, got, err, domain.AssignmentEvent(domain.AssignmentAccepted{
		AssignmentID: humanAssignmentID, WorkID: workID, Stage: domain.StageDiscovery, Visit: visit1,
	}))
	stateIs[domain.Accepted](t, thenAssignmentState(t, given, got))
}

func TestApprove_WithoutOwner_IsRejected(t *testing.T) {
	waiting := stateIs[domain.AwaitingApproval](t, givenAssignment(t, history(openedOnHumanGate(), events(awaitingApproval(1)))...))

	got, err := waiting.Approve(domain.Owner{})

	thenRejected(t, got, err, domain.ErrMissingValue)
}

func TestReject_GoesBackToOpenAndUsesAnAttempt(t *testing.T) {
	given := history(openedOnHumanGate(), events(awaitingApproval(1)))
	waiting := stateIs[domain.AwaitingApproval](t, givenAssignment(t, given...))

	got, err := waiting.Reject(owner, feedback)

	thenEvents(t, got, err, rejected(1))
	open := stateIs[domain.Open](t, thenAssignmentState(t, given, got))
	assert.Equal(t, attempts(1), open.AttemptsUsed())
}

func TestReject_ReachingTheBudget_Escalates(t *testing.T) {
	given := history(openedOnHumanGate(), events(
		failed(humanAssignmentID, 1),
		awaitingApproval(2), rejected(2),
		awaitingApproval(3),
	))
	waiting := stateIs[domain.AwaitingApproval](t, givenAssignment(t, given...))

	got, err := waiting.Reject(owner, feedback)

	thenEvents(t, got, err, rejected(3), escalated(humanAssignmentID, 3, 3))
	stateIs[domain.Escalated](t, thenAssignmentState(t, given, got))
}

func TestExtendBudget_ReopensWithTheNewBudget(t *testing.T) {
	given := history(openedOnAutoGate(), events(
		failed(autoAssignmentID, 1), failed(autoAssignmentID, 2), failed(autoAssignmentID, 3),
		escalated(autoAssignmentID, 3, 3),
	))
	esc := stateIs[domain.Escalated](t, givenAssignment(t, given...))

	got, err := esc.ExtendBudget(owner, budget(2))

	thenEvents(t, got, err, domain.AssignmentEvent(domain.BudgetExtended{
		AssignmentID: autoAssignmentID, Owner: owner, Additional: budget(2), NewBudget: budget(5),
	}))
	open := stateIs[domain.Open](t, thenAssignmentState(t, given, got))
	assert.Equal(t, budget(5), open.Budget())
	assert.Equal(t, attempts(3), open.AttemptsUsed())

	// The extension gives exactly the additional attempts.
	full := append(append(given, got...), failed(autoAssignmentID, 4))
	open = stateIs[domain.Open](t, givenAssignment(t, full...))
	got, err = open.SubmitForAcceptance(domain.ClaimFailed(reason), noChanges)
	thenEvents(t, got, err, failed(autoAssignmentID, 5), escalated(autoAssignmentID, 5, 5))
}

func TestCancel_FromEachCancellableState(t *testing.T) {
	cancelReason := reasonOf("work abandoned")
	cancelled := func(id domain.AssignmentID) domain.AssignmentEvent {
		return domain.AssignmentCancelled{AssignmentID: id, Reason: cancelReason}
	}

	t.Run("open", func(t *testing.T) {
		s := stateIs[domain.Open](t, givenAssignment(t, openedOnAutoGate()...))
		got, err := s.Cancel(cancelReason)
		thenEvents(t, got, err, cancelled(autoAssignmentID))
		stateIs[domain.Cancelled](t, thenAssignmentState(t, openedOnAutoGate(), got))
	})
	t.Run("awaiting approval", func(t *testing.T) {
		given := history(openedOnHumanGate(), events(awaitingApproval(1)))
		s := stateIs[domain.AwaitingApproval](t, givenAssignment(t, given...))
		got, err := s.Cancel(cancelReason)
		thenEvents(t, got, err, cancelled(humanAssignmentID))
		stateIs[domain.Cancelled](t, thenAssignmentState(t, given, got))
	})
	t.Run("escalated", func(t *testing.T) {
		given := history(openedOnAutoGate(), events(
			failed(autoAssignmentID, 1), failed(autoAssignmentID, 2), failed(autoAssignmentID, 3),
			escalated(autoAssignmentID, 3, 3),
		))
		s := stateIs[domain.Escalated](t, givenAssignment(t, given...))
		got, err := s.Cancel(cancelReason)
		thenEvents(t, got, err, cancelled(autoAssignmentID))
		stateIs[domain.Cancelled](t, thenAssignmentState(t, given, got))
	})
}

func TestRebuildAssignment_RejectsInconsistentHistory(t *testing.T) {
	accepted := domain.AssignmentAccepted{AssignmentID: autoAssignmentID, WorkID: workID, Stage: domain.StageImplement, Visit: visit1}
	cases := map[string][]domain.AssignmentEvent{
		"empty":                     nil,
		"no AssignmentOpened":       events(failed(autoAssignmentID, 1)),
		"id not derived":            events(domain.AssignmentOpened{AssignmentID: humanAssignmentID, WorkID: workID, Stage: domain.StageImplement, Visit: visit1, Gate: domain.GateAuto, Budget: budget3}),
		"event of other assignment": history(openedOnAutoGate(), events(failed(humanAssignmentID, 1))),
		"wrong attempt number":      history(openedOnAutoGate(), events(failed(autoAssignmentID, 2))),
		"approval on auto gate":     history(openedOnAutoGate(), events(domain.SubmittedForApproval{AssignmentID: autoAssignmentID, Attempt: attempt(1)})),
		"event after acceptance":    history(openedOnAutoGate(), events(accepted, failed(autoAssignmentID, 1))),
		"budget used, not escalated": history(openedOnAutoGate(), events(
			failed(autoAssignmentID, 1), failed(autoAssignmentID, 2), failed(autoAssignmentID, 3))),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := domain.RebuildAssignment(h)
			require.ErrorIs(t, err, domain.ErrInconsistentHistory)
		})
	}
}
