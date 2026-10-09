// Package domaintest builds past histories of the fix-bug path for tests.
// Every history is about one work, WorkID.
package domaintest

import "github.com/jordiSalazarr/go-fast/workflow/domain"

func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

var (
	WorkID      = Must(domain.NewWorkID("w1"))
	Description = Must(domain.NewDescription("login button does nothing"))
	Owner       = Must(domain.NewOwner("Jordi", "jordi@example.com"))
	Visit1      = domain.FirstVisit()
	Branch      = Must(domain.NewBranch("main"))
)

func Reason(s string) domain.Reason      { return Must(domain.NewReason(s)) }
func Feedback(s string) domain.Feedback  { return Must(domain.NewFeedback(s)) }
func Budget(n int) domain.AttemptBudget  { return Must(domain.NewAttemptBudget(n)) }
func Attempt(n int) domain.Attempt       { return Must(domain.NewAttempt(n)) }
func Attempts(n int) domain.AttemptCount { return Must(domain.NewAttemptCount(n)) }
func AssignmentOn(s domain.Stage) domain.AssignmentID {
	return domain.AssignmentIDFor(WorkID, s, Visit1)
}

func step(stage domain.Stage) domain.PathStep {
	for _, s := range Must(domain.PathFor(domain.WorkTypeFixBug)).Steps() {
		if s.Stage() == stage {
			return s
		}
	}
	panic("stage not on the fix-bug path: " + stage.String())
}

func WorkStarted() domain.WorkStarted {
	return domain.WorkStarted{WorkID: WorkID, WorkType: domain.WorkTypeFixBug, Description: Description, Branch: Branch}
}

func StageEntered(stage domain.Stage) domain.StageEntered {
	s := step(stage)
	return domain.StageEntered{WorkID: WorkID, Stage: stage, Visit: Visit1, Gate: s.Gate(), Budget: s.Budget()}
}

// WorkOn is the work's history up to entering stage.
func WorkOn(stage domain.Stage) []domain.Event {
	events := []domain.Event{WorkStarted()}
	for _, s := range Must(domain.PathFor(domain.WorkTypeFixBug)).Steps() {
		events = append(events, StageEntered(s.Stage()))
		if s.Stage() == stage {
			return events
		}
	}
	panic("stage not on the fix-bug path: " + stage.String())
}

// OpenOn is the work on stage with that stage's assignment open.
func OpenOn(stage domain.Stage, then ...domain.Event) []domain.Event {
	return append(append(WorkOn(stage), Opened(stage)), then...)
}

// Completed is the history of a work that went through the whole path.
func Completed() []domain.Event {
	return append(WorkOn(domain.StageIntegrationTesting), domain.WorkCompleted{WorkID: WorkID})
}

func Opened(stage domain.Stage) domain.AssignmentOpened {
	s := step(stage)
	return domain.AssignmentOpened{AssignmentID: AssignmentOn(stage), WorkID: WorkID, Stage: stage, Visit: Visit1, Gate: s.Gate(), Budget: s.Budget()}
}

func Failed(stage domain.Stage, attempt int, reason string) domain.AttemptFailed {
	return domain.AttemptFailed{AssignmentID: AssignmentOn(stage), Attempt: Attempt(attempt), Reason: Reason(reason)}
}

func SubmittedForApproval(stage domain.Stage, attempt int) domain.SubmittedForApproval {
	return domain.SubmittedForApproval{AssignmentID: AssignmentOn(stage), Attempt: Attempt(attempt)}
}

func Rejected(stage domain.Stage, attempt int, feedback string) domain.AssignmentRejected {
	return domain.AssignmentRejected{AssignmentID: AssignmentOn(stage), Owner: Owner, Feedback: Feedback(feedback), Attempt: Attempt(attempt)}
}

func Escalated(stage domain.Stage, used, budget int) domain.AssignmentEscalated {
	return domain.AssignmentEscalated{AssignmentID: AssignmentOn(stage), AttemptsUsed: Attempts(used), Budget: Budget(budget)}
}

func Accepted(stage domain.Stage) domain.AssignmentAccepted {
	return domain.AssignmentAccepted{AssignmentID: AssignmentOn(stage), WorkID: WorkID, Stage: stage, Visit: Visit1}
}

// EscalatedOn is the stage's assignment after its whole budget of 3 failed.
func EscalatedOn(stage domain.Stage) []domain.Event {
	return OpenOn(stage,
		Failed(stage, 1, "tests fail"), Failed(stage, 2, "tests fail"), Failed(stage, 3, "tests fail"),
		Escalated(stage, 3, 3))
}
