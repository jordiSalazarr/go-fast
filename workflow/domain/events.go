package domain

// Event is a business fact. Events carry no log metadata (IDs, timestamps,
// actors); the event log wraps them in an envelope.
type Event interface{ isEvent() }

// WorkEvent is an event of the Work aggregate.
type WorkEvent interface {
	Event
	workID() WorkID
}

// AssignmentEvent is an event of the Assignment aggregate.
type AssignmentEvent interface {
	Event
	assignmentID() AssignmentID
}

// Work events.

type WorkStarted struct {
	WorkID      WorkID
	WorkType    WorkType
	Description Description
	Branch      Branch
}

// StageEntered carries the gate and budget so an assignment never needs to
// know about paths.
type StageEntered struct {
	WorkID WorkID
	Stage  Stage
	Visit  Visit
	Gate   Gate
	Budget AttemptBudget
}

// FixerEntered: a stage that checks code handed the work over, and the work
// entered the fixer for the Visit-th time. Followed by the fixer's
// StageEntered.
type FixerEntered struct {
	WorkID    WorkID
	From      Stage
	FromVisit Visit
	Visit     Visit
	Mode      FixerMode
	Reason    Reason
}

// ReworkLimitReached: a stage handed the work over, but it already entered
// the fixer as often as its rework budget allows.
type ReworkLimitReached struct {
	WorkID  WorkID
	Reworks ReworkCount
}

// ReworkStarted: the fixer returned the work to an earlier stage, which it
// enters again on a new visit. Followed by that stage's StageEntered.
type ReworkStarted struct {
	WorkID WorkID
	Target Stage
	Visit  Visit
	Reason Reason
}

// ReworkBudgetExtended: the owner allowed more reworks.
type ReworkBudgetExtended struct {
	WorkID     WorkID
	Owner      Owner
	Additional ReworkBudget
	NewBudget  ReworkBudget
}

type WorkCompleted struct {
	WorkID WorkID
}

type WorkAbandoned struct {
	WorkID WorkID
	Owner  Owner
	Reason Reason
}

// Assignment events.

type AssignmentOpened struct {
	AssignmentID AssignmentID
	WorkID       WorkID
	Stage        Stage
	Visit        Visit
	Gate         Gate
	Budget       AttemptBudget
}

type AttemptFailed struct {
	AssignmentID AssignmentID
	Attempt      Attempt
	Reason       Reason
}

type SubmittedForApproval struct {
	AssignmentID AssignmentID
	Attempt      Attempt
}

type AssignmentRejected struct {
	AssignmentID AssignmentID
	Owner        Owner
	Feedback     Feedback
	Attempt      Attempt
}

type AssignmentEscalated struct {
	AssignmentID AssignmentID
	AttemptsUsed AttemptCount
	Budget       AttemptBudget
}

type BudgetExtended struct {
	AssignmentID AssignmentID
	Owner        Owner
	Additional   AttemptBudget
	NewBudget    AttemptBudget
}

// AssignmentAccepted carries the fixer's route when the fixer is accepted;
// for every other stage the route is zero.
type AssignmentAccepted struct {
	AssignmentID AssignmentID
	WorkID       WorkID
	Stage        Stage
	Visit        Visit
	Route        Route
}

// AssignmentHandedToFixer: the stage visit failed because the code is wrong,
// which the stage can't fix, so the work goes to the fixer. It uses no
// attempt.
type AssignmentHandedToFixer struct {
	AssignmentID AssignmentID
	WorkID       WorkID
	Stage        Stage
	Visit        Visit
	Attempt      Attempt
	Reason       Reason
}

type AssignmentCancelled struct {
	AssignmentID AssignmentID
	Reason       Reason
}

func (WorkStarted) isEvent()             {}
func (StageEntered) isEvent()            {}
func (WorkCompleted) isEvent()           {}
func (WorkAbandoned) isEvent()           {}
func (FixerEntered) isEvent()            {}
func (ReworkLimitReached) isEvent()      {}
func (ReworkStarted) isEvent()           {}
func (ReworkBudgetExtended) isEvent()    {}
func (AssignmentOpened) isEvent()        {}
func (AttemptFailed) isEvent()           {}
func (SubmittedForApproval) isEvent()    {}
func (AssignmentRejected) isEvent()      {}
func (AssignmentEscalated) isEvent()     {}
func (BudgetExtended) isEvent()          {}
func (AssignmentAccepted) isEvent()      {}
func (AssignmentCancelled) isEvent()     {}
func (AssignmentHandedToFixer) isEvent() {}

func (e WorkStarted) workID() WorkID          { return e.WorkID }
func (e StageEntered) workID() WorkID         { return e.WorkID }
func (e WorkCompleted) workID() WorkID        { return e.WorkID }
func (e WorkAbandoned) workID() WorkID        { return e.WorkID }
func (e FixerEntered) workID() WorkID         { return e.WorkID }
func (e ReworkLimitReached) workID() WorkID   { return e.WorkID }
func (e ReworkStarted) workID() WorkID        { return e.WorkID }
func (e ReworkBudgetExtended) workID() WorkID { return e.WorkID }

func (e AssignmentOpened) assignmentID() AssignmentID        { return e.AssignmentID }
func (e AttemptFailed) assignmentID() AssignmentID           { return e.AssignmentID }
func (e SubmittedForApproval) assignmentID() AssignmentID    { return e.AssignmentID }
func (e AssignmentRejected) assignmentID() AssignmentID      { return e.AssignmentID }
func (e AssignmentEscalated) assignmentID() AssignmentID     { return e.AssignmentID }
func (e BudgetExtended) assignmentID() AssignmentID          { return e.AssignmentID }
func (e AssignmentAccepted) assignmentID() AssignmentID      { return e.AssignmentID }
func (e AssignmentCancelled) assignmentID() AssignmentID     { return e.AssignmentID }
func (e AssignmentHandedToFixer) assignmentID() AssignmentID { return e.AssignmentID }
