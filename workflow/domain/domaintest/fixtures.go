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
	if stage == domain.StageFixer {
		return domain.FixerStep()
	}
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

// StressSkipped is review's choice not to stress test, which no NFR forces.
var StressSkipped = Must(domain.NewStressChoice(domain.StressSkip, Reason("no change affects behaviour under load"), nil))

// StressIncluded is review's choice to stress test, forced by no NFR.
var StressIncluded = Must(domain.NewStressChoice(domain.StressInclude, Reason("the change touches a hot path"), nil))

// StressDecided is review's decision, recorded as the work advances past it.
func StressDecided(choice domain.StressChoice) domain.StressDecided {
	return domain.StressDecided{WorkID: WorkID, Decision: choice.Decision(), Reason: choice.Reason(), ForcedBy: choice.ForcedBy()}
}

// WorkOn is the work's history up to entering stage. Review decides to skip
// stress testing, unless stage is stress testing.
func WorkOn(stage domain.Stage) []domain.Event {
	events := []domain.Event{WorkStarted()}
	choice := StressSkipped
	if stage == domain.StageStressTesting {
		choice = StressIncluded
	}
	for _, s := range Must(domain.PathFor(domain.WorkTypeFixBug)).Steps() {
		if s.Optional() && stage != s.Stage() {
			continue
		}
		events = append(events, StageEntered(s.Stage()))
		if s.Stage() == stage {
			return events
		}
		if s.Stage() == domain.StressDecider() {
			events = append(events, StressDecided(choice))
		}
	}
	panic("stage not on the fix-bug path: " + stage.String())
}

// OpenOn is the work on stage with that stage's assignment open.
func OpenOn(stage domain.Stage, then ...domain.Event) []domain.Event {
	return append(append(WorkOn(stage), Opened(stage)), then...)
}

// Completed is the history of a work that went through the whole path,
// without stress testing.
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

// Accepted is the stage's first assignment accepted; review's carries the
// choice to skip stress testing.
func Accepted(stage domain.Stage) domain.AssignmentAccepted {
	accepted := domain.AssignmentAccepted{AssignmentID: AssignmentOn(stage), WorkID: WorkID, Stage: stage, Visit: Visit1}
	if stage == domain.StressDecider() {
		accepted.Stress = StressSkipped
	}
	return accepted
}

// EscalatedOn is the stage's assignment after its whole budget of 3 failed.
func EscalatedOn(stage domain.Stage) []domain.Event {
	return OpenOn(stage,
		Failed(stage, 1, "tests fail"), Failed(stage, 2, "tests fail"), Failed(stage, 3, "tests fail"),
		Escalated(stage, 3, 3))
}

// HandedToFixer is the stage's first assignment handing the work to the fixer.
func HandedToFixer(stage domain.Stage, attempt int, reason string) domain.AssignmentHandedToFixer {
	return domain.AssignmentHandedToFixer{AssignmentID: AssignmentOn(stage), WorkID: WorkID, Stage: stage, Visit: Visit1, Attempt: Attempt(attempt), Reason: Reason(reason)}
}

// FixerEntered is the work entering the fixer for the first time, from the
// first visit of stage.
func FixerEntered(from domain.Stage, reason string) []domain.Event {
	return []domain.Event{
		domain.FixerEntered{WorkID: WorkID, From: from, FromVisit: Visit1, Visit: Visit1, Mode: domain.FixerModeCorrectness, Reason: Reason(reason)},
		StageEntered(domain.StageFixer),
	}
}

// HandedOverOn is the work on stage, whose assignment handed it to the fixer.
func HandedOverOn(stage domain.Stage, reason string) []domain.Event {
	return OpenOn(stage, HandedToFixer(stage, 1, reason))
}

// InFixer is the work in its first fixer visit, handed over by stage, with
// the fixer's assignment open.
func InFixer(from domain.Stage) []domain.Event {
	return append(append(HandedOverOn(from, "suite fails"), FixerEntered(from, "suite fails")...), Opened(domain.StageFixer))
}

// Route is a route to target.
func Route(target domain.Stage, reason string) domain.Route {
	return Must(domain.NewRoute(target, Reason(reason)))
}

func Visit(n int) domain.Visit { return Must(domain.NewVisit(n)) }

// EnteredVisit is the StageEntered of a stage's nth visit.
func EnteredVisit(stage domain.Stage, n int) domain.StageEntered {
	e := StageEntered(stage)
	e.Visit = Visit(n)
	return e
}

// HandedToFixerOnVisit is the nth visit of stage handing the work to the
// fixer on its first attempt.
func HandedToFixerOnVisit(stage domain.Stage, n int, reason string) domain.AssignmentHandedToFixer {
	return domain.AssignmentHandedToFixer{
		AssignmentID: domain.AssignmentIDFor(WorkID, stage, Visit(n)), WorkID: WorkID, Stage: stage, Visit: Visit(n),
		Attempt: Attempt(1), Reason: Reason(reason),
	}
}

// ReworkedOnReview is the work's history (its own stream only) after review
// handed it over n times and the fixer sent it back to implement each time:
// it is on review's visit n+1.
func ReworkedOnReview(n int) []domain.Event {
	events := WorkOn(domain.StageReview)
	for i := 1; i <= n; i++ {
		events = append(events,
			domain.FixerEntered{WorkID: WorkID, From: domain.StageReview, FromVisit: Visit(i), Visit: Visit(i), Mode: domain.FixerModeCorrectness, Reason: Reason("suite fails")},
			EnteredVisit(domain.StageFixer, i),
			domain.ReworkStarted{WorkID: WorkID, Target: domain.StageImplement, Visit: Visit(i + 1), Reason: Reason("the fix misses a case")},
			EnteredVisit(domain.StageImplement, i+1),
			EnteredVisit(domain.StageReview, i+1),
		)
	}
	return events
}

// OpenedVisit is the assignment of a stage's nth visit, opened.
func OpenedVisit(stage domain.Stage, n int) domain.AssignmentOpened {
	o := Opened(stage)
	o.AssignmentID, o.Visit = domain.AssignmentIDFor(WorkID, stage, Visit(n)), Visit(n)
	return o
}
