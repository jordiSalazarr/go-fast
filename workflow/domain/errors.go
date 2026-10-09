package domain

import (
	"errors"
	"fmt"
)

// Invalid values.
var (
	ErrInvalidWorkID         = errors.New("invalid work id")
	ErrInvalidAssignmentID   = errors.New("invalid assignment id")
	ErrUnknownWorkType       = errors.New("unknown work type")
	ErrUnknownStage          = errors.New("unknown stage")
	ErrUnknownGate           = errors.New("unknown gate")
	ErrInvalidAttemptBudget  = errors.New("attempt budget must be a positive number")
	ErrInvalidAttempt        = errors.New("invalid attempt number")
	ErrInvalidVisit          = errors.New("visit must be a positive number")
	ErrEmptyDescription      = errors.New("description must not be empty")
	ErrEmptyFeedback         = errors.New("feedback must not be empty")
	ErrEmptyReason           = errors.New("reason must not be empty")
	ErrInvalidOwner          = errors.New("owner must have a name")
	ErrMissingValue          = errors.New("required value is missing")
	ErrInvalidRepoPath       = errors.New("invalid repository path")
	ErrPathOutsideRepository = errors.New("path is outside the repository")
)

// Rejected commands.
var (
	ErrWorkAlreadyActive = errors.New("work is already active in this repository")
	ErrNotCurrentStage   = errors.New("stage is not the current stage of the work")
	ErrNoActiveWork      = errors.New("no active work in this repository")
)

// The current assignment is not in a state that accepts the command.
var (
	ErrNotAwaitingSubmission = errors.New("assignment is not open for a submission")
	ErrNotAwaitingApproval   = errors.New("assignment is not awaiting approval")
	ErrNotEscalated          = errors.New("assignment is not escalated")
)

// ErrInconsistentHistory means a stream's events cannot have been produced by
// the domain: the log is corrupt or was edited by hand.
var ErrInconsistentHistory = errors.New("inconsistent event history")

// WorkAlreadyActiveError names the work that blocks starting a new one.
type WorkAlreadyActiveError struct {
	ID          WorkID
	Type        WorkType
	Description Description
}

func (e *WorkAlreadyActiveError) Error() string {
	return fmt.Sprintf("%s: %s %q (%s)", ErrWorkAlreadyActive, e.Type, e.Description, e.ID)
}

func (e *WorkAlreadyActiveError) Unwrap() error { return ErrWorkAlreadyActive }

// NotCurrentStageError says which stage visit was asked for and which is current.
type NotCurrentStageError struct {
	Requested      Stage
	RequestedVisit Visit
	Current        Stage
	CurrentVisit   Visit
}

func (e *NotCurrentStageError) Error() string {
	return fmt.Sprintf("%s: requested %s (visit %d), current is %s (visit %d)",
		ErrNotCurrentStage, e.Requested, e.RequestedVisit.n, e.Current, e.CurrentVisit.n)
}

func (e *NotCurrentStageError) Unwrap() error { return ErrNotCurrentStage }
