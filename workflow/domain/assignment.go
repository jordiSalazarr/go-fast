package domain

import "fmt"

// AssignmentState is the state of an Assignment rebuilt from its events.
type AssignmentState interface {
	ID() AssignmentID
	isAssignmentState()
}

type assignment struct {
	id           AssignmentID
	work         WorkID
	stage        Stage
	visit        Visit
	gate         Gate
	budget       AttemptBudget
	attemptsUsed AttemptCount
}

func (a assignment) ID() AssignmentID           { return a.id }
func (a assignment) Work() WorkID               { return a.work }
func (a assignment) Stage() Stage               { return a.stage }
func (a assignment) Visit() Visit               { return a.visit }
func (a assignment) Gate() Gate                 { return a.gate }
func (a assignment) Budget() AttemptBudget      { return a.budget }
func (a assignment) AttemptsUsed() AttemptCount { return a.attemptsUsed }

// Open waits for a submission.
type Open struct{ assignment }

// AwaitingApproval passed its exit check on a human gate and waits for the owner.
type AwaitingApproval struct {
	assignment
	attempt Attempt
}

func (a AwaitingApproval) Attempt() Attempt { return a.attempt }

// Escalated used its whole budget; only the owner can unblock it.
type Escalated struct{ assignment }

// Accepted got its stage visit accepted.
type Accepted struct{ assignment }

// Cancelled was cancelled because its work was abandoned.
type Cancelled struct {
	assignment
	reason Reason
}

func (a Cancelled) Reason() Reason { return a.reason }

func (Open) isAssignmentState()             {}
func (AwaitingApproval) isAssignmentState() {}
func (Escalated) isAssignmentState()        {}
func (Accepted) isAssignmentState()         {}
func (Cancelled) isAssignmentState()        {}

// OpenAssignment opens the assignment for one stage visit.
func OpenAssignment(work WorkID, stage Stage, visit Visit, gate Gate, budget AttemptBudget) ([]AssignmentEvent, error) {
	if err := requirePresent(
		required{"work id", work}, required{"stage", stage}, required{"visit", visit},
		required{"gate", gate}, required{"budget", budget},
	); err != nil {
		return nil, fmt.Errorf("open assignment: %w", err)
	}
	return []AssignmentEvent{AssignmentOpened{
		AssignmentID: AssignmentIDFor(work, stage, visit),
		WorkID:       work, Stage: stage, Visit: visit, Gate: gate, Budget: budget,
	}}, nil
}

// SubmitForAcceptance interprets the stage's exit check: the agent's claim,
// and the files the stage visit changed checked against its write scope. A
// failed check uses an attempt; a passing one is accepted on an auto gate and
// waits for the owner on a human gate.
func (a Open) SubmitForAcceptance(claim AgentClaim, changed ChangedFiles) ([]AssignmentEvent, error) {
	if err := requirePresent(required{"agent claim", claim}); err != nil {
		return nil, fmt.Errorf("submit assignment %s for acceptance: %w", a.id, err)
	}
	outcome := NewExitCheckOutcome(claim, CheckWriteScope(a.stage, a.work, changed))
	attempt := a.attemptsUsed.currentAttempt()
	switch {
	case !outcome.Passed():
		failed := AttemptFailed{AssignmentID: a.id, Attempt: attempt, Reason: outcome.Reason()}
		return a.withEscalationIfExhausted(failed), nil
	case a.gate == GateHuman:
		return []AssignmentEvent{SubmittedForApproval{AssignmentID: a.id, Attempt: attempt}}, nil
	default:
		return []AssignmentEvent{a.accepted()}, nil
	}
}

func (a Open) Cancel(reason Reason) ([]AssignmentEvent, error) { return a.cancel(reason) }

// Approve accepts the submission awaiting approval.
func (a AwaitingApproval) Approve(owner Owner) ([]AssignmentEvent, error) {
	if err := requirePresent(required{"owner", owner}); err != nil {
		return nil, fmt.Errorf("approve assignment %s: %w", a.id, err)
	}
	return []AssignmentEvent{a.accepted()}, nil
}

// Reject sends the submission back with feedback; it uses one attempt.
func (a AwaitingApproval) Reject(owner Owner, feedback Feedback) ([]AssignmentEvent, error) {
	if err := requirePresent(required{"owner", owner}, required{"feedback", feedback}); err != nil {
		return nil, fmt.Errorf("reject assignment %s: %w", a.id, err)
	}
	rejected := AssignmentRejected{AssignmentID: a.id, Owner: owner, Feedback: feedback, Attempt: a.attempt}
	return a.withEscalationIfExhausted(rejected), nil
}

func (a AwaitingApproval) Cancel(reason Reason) ([]AssignmentEvent, error) { return a.cancel(reason) }

// ExtendBudget gives an escalated assignment more attempts.
func (a Escalated) ExtendBudget(owner Owner, additional AttemptBudget) ([]AssignmentEvent, error) {
	if err := requirePresent(required{"owner", owner}, required{"additional attempts", additional}); err != nil {
		return nil, fmt.Errorf("extend budget of assignment %s: %w", a.id, err)
	}
	return []AssignmentEvent{BudgetExtended{
		AssignmentID: a.id, Owner: owner, Additional: additional, NewBudget: a.budget.extendedBy(additional),
	}}, nil
}

func (a Escalated) Cancel(reason Reason) ([]AssignmentEvent, error) { return a.cancel(reason) }

// withEscalationIfExhausted follows an attempt-using event with an escalation
// when that attempt was the last of the budget.
func (a assignment) withEscalationIfExhausted(used AssignmentEvent) []AssignmentEvent {
	events := []AssignmentEvent{used}
	if usedNow := a.attemptsUsed.plusOne(); a.budget.exhaustedBy(usedNow) {
		events = append(events, AssignmentEscalated{AssignmentID: a.id, AttemptsUsed: usedNow, Budget: a.budget})
	}
	return events
}

func (a assignment) accepted() AssignmentAccepted {
	return AssignmentAccepted{AssignmentID: a.id, WorkID: a.work, Stage: a.stage, Visit: a.visit}
}

func (a assignment) cancel(reason Reason) ([]AssignmentEvent, error) {
	if err := requirePresent(required{"reason", reason}); err != nil {
		return nil, fmt.Errorf("cancel assignment %s: %w", a.id, err)
	}
	return []AssignmentEvent{AssignmentCancelled{AssignmentID: a.id, Reason: reason}}, nil
}

// RebuildAssignment rebuilds an assignment's state from its stream.
func RebuildAssignment(history []AssignmentEvent) (AssignmentState, error) {
	if len(history) == 0 {
		return nil, fmt.Errorf("rebuild assignment: empty stream: %w", ErrInconsistentHistory)
	}
	opened, ok := history[0].(AssignmentOpened)
	if !ok {
		return nil, fmt.Errorf("rebuild assignment: first event is %T, want AssignmentOpened: %w", history[0], ErrInconsistentHistory)
	}
	if opened.AssignmentID != AssignmentIDFor(opened.WorkID, opened.Stage, opened.Visit) {
		return nil, fmt.Errorf("rebuild assignment %s: id is not derived from its stage visit: %w", opened.AssignmentID, ErrInconsistentHistory)
	}
	var state AssignmentState = Open{assignment{
		id: opened.AssignmentID, work: opened.WorkID, stage: opened.Stage,
		visit: opened.Visit, gate: opened.Gate, budget: opened.Budget,
	}}
	for i, e := range history[1:] {
		if e.assignmentID() != opened.AssignmentID {
			return nil, fmt.Errorf("rebuild assignment %s: event %d belongs to assignment %s: %w", opened.AssignmentID, i+2, e.assignmentID(), ErrInconsistentHistory)
		}
		next, ok := evolveAssignment(state, e)
		if !ok {
			return nil, fmt.Errorf("rebuild assignment %s: event %d (%T) not possible in state %T: %w", opened.AssignmentID, i+2, e, state, ErrInconsistentHistory)
		}
		state = next
	}
	// An attempt that exhausts the budget is always recorded with its escalation.
	if open, ok := state.(Open); ok && open.budget.exhaustedBy(open.attemptsUsed) {
		return nil, fmt.Errorf("rebuild assignment %s: budget used up without escalation: %w", opened.AssignmentID, ErrInconsistentHistory)
	}
	return state, nil
}

func evolveAssignment(state AssignmentState, e AssignmentEvent) (AssignmentState, bool) {
	switch s := state.(type) {
	case Open:
		attempt := s.attemptsUsed.currentAttempt()
		switch e := e.(type) {
		case AttemptFailed:
			if e.Attempt != attempt {
				return nil, false
			}
			s.attemptsUsed = s.attemptsUsed.plusOne()
			return s, true
		case SubmittedForApproval:
			if e.Attempt != attempt || s.gate != GateHuman {
				return nil, false
			}
			return AwaitingApproval{assignment: s.assignment, attempt: e.Attempt}, true
		case AssignmentAccepted:
			if s.gate != GateAuto {
				return nil, false
			}
			return Accepted{s.assignment}, true
		case AssignmentEscalated:
			if !s.budget.exhaustedBy(s.attemptsUsed) || e.AttemptsUsed != s.attemptsUsed {
				return nil, false
			}
			return Escalated{s.assignment}, true
		case AssignmentCancelled:
			return Cancelled{assignment: s.assignment, reason: e.Reason}, true
		}
	case AwaitingApproval:
		switch e := e.(type) {
		case AssignmentAccepted:
			return Accepted{s.assignment}, true
		case AssignmentRejected:
			if e.Attempt != s.attempt {
				return nil, false
			}
			s.attemptsUsed = s.attemptsUsed.plusOne()
			return Open{s.assignment}, true
		case AssignmentCancelled:
			return Cancelled{assignment: s.assignment, reason: e.Reason}, true
		}
	case Escalated:
		switch e := e.(type) {
		case BudgetExtended:
			if e.NewBudget != s.budget.extendedBy(e.Additional) {
				return nil, false
			}
			s.budget = e.NewBudget
			return Open{s.assignment}, true
		case AssignmentCancelled:
			return Cancelled{assignment: s.assignment, reason: e.Reason}, true
		}
	}
	return nil, false
}
