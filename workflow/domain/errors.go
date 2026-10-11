package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Invalid values.
var (
	ErrInvalidWorkID         = errors.New("invalid work id")
	ErrInvalidAssignmentID   = errors.New("invalid assignment id")
	ErrUnknownWorkType       = errors.New("unknown work type")
	ErrUnknownStage          = errors.New("unknown stage")
	ErrUnknownGate           = errors.New("unknown gate")
	ErrInvalidAttemptBudget  = errors.New("attempt budget must be a positive number")
	ErrInvalidReworkBudget   = errors.New("rework budget must be a positive number")
	ErrUnknownFixerMode      = errors.New("unknown fixer mode")
	ErrUnknownStressDecision = errors.New("unknown stress decision")
	ErrInvalidAttempt        = errors.New("invalid attempt number")
	ErrInvalidVisit          = errors.New("visit must be a positive number")
	ErrEmptyDescription      = errors.New("description must not be empty")
	ErrEmptyFeedback         = errors.New("feedback must not be empty")
	ErrEmptyReason           = errors.New("reason must not be empty")
	ErrEmptyCheckSummary     = errors.New("a failed check's summary must not be empty")
	ErrInvalidOwner          = errors.New("owner must have a name")
	ErrMissingValue          = errors.New("required value is missing")
	ErrInvalidBranch         = errors.New("branch name must not be empty or contain whitespace")
	ErrInvalidRepoPath       = errors.New("invalid repository path")
	ErrPathOutsideRepository = errors.New("path is outside the repository")
	ErrInvalidNFR            = errors.New("invalid non-functional requirement")
	ErrInvalidMeasurement    = errors.New("invalid measurement")
	ErrInvalidDesign         = errors.New("invalid design")
)

// Rejected commands.
var (
	ErrWorkAlreadyActive = errors.New("work is already active on this branch")
	ErrNotCurrentStage   = errors.New("stage is not the current stage of the work")
	ErrNoActiveWork      = errors.New("no active work")
	ErrCannotEnterFixer  = errors.New("only a stage that checks code can hand work to the fixer")
	ErrInvalidRoute      = errors.New("the fixer can only return work to an earlier stage on its path that can change code")
	ErrMissingRoute      = errors.New("the fixer's pass must say which stage the work returns to")
	ErrRouteOutsideFixer = errors.New("only the fixer chooses a route")
	// Stress testing.
	ErrMissingStressDecision = errors.New("review's pass must decide whether stress testing runs")
	ErrStressOutsideReview   = errors.New("only review decides stress testing when it submits")
	ErrStressForced          = errors.New("stress testing is mandatory: the change touches an NFR's areas")
	ErrStressUndecided       = errors.New("the work has no stress decision")
	ErrStressDecisionClosed  = errors.New("the stress decision can only change while the work is on a stage up to integration testing")
)

// The current assignment is not in a state that accepts the command.
var (
	ErrNotAwaitingSubmission = errors.New("assignment is not open for a submission")
	ErrNotAwaitingApproval   = errors.New("assignment is not awaiting approval")
	ErrNotEscalated          = errors.New("assignment is not escalated")
	ErrNotAtReworkLimit      = errors.New("work has not reached its rework limit")
)

// ErrInconsistentHistory means a stream's events cannot have been produced by
// the domain: the log is corrupt or was edited by hand.
var ErrInconsistentHistory = errors.New("inconsistent event history")

// WorkAlreadyActiveError names the work that blocks starting a new one.
type WorkAlreadyActiveError struct {
	ID          WorkID
	Type        WorkType
	Description Description
	Branch      Branch
}

func (e *WorkAlreadyActiveError) Error() string {
	return fmt.Sprintf("%s: %s %q (%s) on branch %s", ErrWorkAlreadyActive, e.Type, e.Description, e.ID, e.Branch)
}

func (e *WorkAlreadyActiveError) Unwrap() error { return ErrWorkAlreadyActive }

// ConflictingActiveWorkError names the works in progress on one branch, which
// only a bad merge or a hand edit of the log can produce.
type ConflictingActiveWorkError struct {
	Branch Branch
	Works  []WorkID
}

func (e *ConflictingActiveWorkError) Error() string {
	ids := make([]string, len(e.Works))
	for i, id := range e.Works {
		ids[i] = id.value
	}
	return fmt.Sprintf("%s: branch %s has %d works in progress: %s", ErrInconsistentHistory, e.Branch, len(ids), strings.Join(ids, ", "))
}

func (e *ConflictingActiveWorkError) Unwrap() error { return ErrInconsistentHistory }

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

// InvalidRouteError names the stage the fixer asked for and the ones it may
// return the work to.
type InvalidRouteError struct {
	Target  Stage
	Allowed []Stage // in path order
}

func (e *InvalidRouteError) Error() string {
	names := make([]string, len(e.Allowed))
	for i, s := range e.Allowed {
		names[i] = s.name
	}
	return fmt.Sprintf("%s: %s is not one of %s", ErrInvalidRoute, e.Target, strings.Join(names, ", "))
}

func (e *InvalidRouteError) Unwrap() error { return ErrInvalidRoute }

// RouteMissingError names the stages the fixer may return the work to, when
// it passed without saying which.
type RouteMissingError struct {
	Allowed []Stage // in path order
}

func (e *RouteMissingError) Error() string {
	names := make([]string, len(e.Allowed))
	for i, s := range e.Allowed {
		names[i] = s.name
	}
	return fmt.Sprintf("%s: one of %s", ErrMissingRoute, strings.Join(names, ", "))
}

func (e *RouteMissingError) Unwrap() error { return ErrMissingRoute }
