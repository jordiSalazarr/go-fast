package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Every value type has a validating constructor. The zero value of each type
// is never valid; commands reject zero values with ErrMissingValue.

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// WorkID identifies one piece of work.
type WorkID struct{ value string }

func NewWorkID(s string) (WorkID, error) {
	if !idPattern.MatchString(s) {
		return WorkID{}, fmt.Errorf("work id %q: %w", s, ErrInvalidWorkID)
	}
	return WorkID{value: s}, nil
}

func (id WorkID) String() string { return id.value }
func (id WorkID) isZero() bool   { return id.value == "" }

// AssignmentID identifies one stage visit of a work. It is derived from the
// work, stage and visit, so the same stage visit always has the same ID.
type AssignmentID struct{ value string }

// AssignmentIDFor derives the assignment ID for a stage visit.
func AssignmentIDFor(work WorkID, stage Stage, visit Visit) AssignmentID {
	return AssignmentID{value: work.value + "-" + stage.name + "-" + strconv.Itoa(visit.n)}
}

// ParseAssignmentID restores a previously derived assignment ID.
func ParseAssignmentID(s string) (AssignmentID, error) {
	if !idPattern.MatchString(s) {
		return AssignmentID{}, fmt.Errorf("assignment id %q: %w", s, ErrInvalidAssignmentID)
	}
	return AssignmentID{value: s}, nil
}

func (id AssignmentID) String() string { return id.value }
func (id AssignmentID) isZero() bool   { return id.value == "" }

// WorkType is the kind of work; it determines the path.
type WorkType struct{ name string }

var WorkTypeFixBug = WorkType{name: "fix-bug"}

// WorkTypes lists every known work type.
func WorkTypes() []WorkType { return []WorkType{WorkTypeFixBug} }

func ParseWorkType(s string) (WorkType, error) {
	for _, t := range WorkTypes() {
		if t.name == s {
			return t, nil
		}
	}
	return WorkType{}, fmt.Errorf("work type %q (available: %s): %w", s, joinNames(WorkTypes()), ErrUnknownWorkType)
}

func (t WorkType) String() string { return t.name }
func (t WorkType) isZero() bool   { return t.name == "" }

// Stage is one step of a path.
type Stage struct{ name string }

var (
	StageDiscovery          = Stage{name: "discovery"}
	StageSpecify            = Stage{name: "specify"}
	StageImplement          = Stage{name: "implement"}
	StageReview             = Stage{name: "review"}
	StageIntegrationTesting = Stage{name: "integration-testing"}
)

func Stages() []Stage {
	return []Stage{StageDiscovery, StageSpecify, StageImplement, StageReview, StageIntegrationTesting}
}

func ParseStage(s string) (Stage, error) {
	for _, st := range Stages() {
		if st.name == s {
			return st, nil
		}
	}
	return Stage{}, fmt.Errorf("stage %q (available: %s): %w", s, joinNames(Stages()), ErrUnknownStage)
}

func (s Stage) String() string { return s.name }
func (s Stage) isZero() bool   { return s.name == "" }

// Gate says who decides that a passing stage visit is accepted.
type Gate struct{ name string }

var (
	GateAuto  = Gate{name: "auto"}
	GateHuman = Gate{name: "human"}
)

func ParseGate(s string) (Gate, error) {
	for _, g := range []Gate{GateAuto, GateHuman} {
		if g.name == s {
			return g, nil
		}
	}
	return Gate{}, fmt.Errorf("gate %q (available: auto, human): %w", s, ErrUnknownGate)
}

func (g Gate) String() string { return g.name }
func (g Gate) isZero() bool   { return g.name == "" }

// AttemptBudget is a positive number of attempts.
type AttemptBudget struct{ n int }

func NewAttemptBudget(n int) (AttemptBudget, error) {
	if n < 1 {
		return AttemptBudget{}, fmt.Errorf("attempt budget %d: %w", n, ErrInvalidAttemptBudget)
	}
	return AttemptBudget{n: n}, nil
}

func (b AttemptBudget) Int() int     { return b.n }
func (b AttemptBudget) isZero() bool { return b.n == 0 }

func (b AttemptBudget) exhaustedBy(used AttemptCount) bool { return used.n >= b.n }

func (b AttemptBudget) extendedBy(additional AttemptBudget) AttemptBudget {
	return AttemptBudget{n: b.n + additional.n}
}

// Attempt is the ordinal of an attempt within an assignment, starting at 1.
type Attempt struct{ n int }

func NewAttempt(n int) (Attempt, error) {
	if n < 1 {
		return Attempt{}, fmt.Errorf("attempt %d: %w", n, ErrInvalidAttempt)
	}
	return Attempt{n: n}, nil
}

func (a Attempt) Int() int { return a.n }

// AttemptCount is how many attempts of a budget have been used.
type AttemptCount struct{ n int }

func NewAttemptCount(n int) (AttemptCount, error) {
	if n < 0 {
		return AttemptCount{}, fmt.Errorf("attempt count %d: %w", n, ErrInvalidAttempt)
	}
	return AttemptCount{n: n}, nil
}

func (c AttemptCount) Int() int { return c.n }

func (c AttemptCount) currentAttempt() Attempt { return Attempt{n: c.n + 1} }
func (c AttemptCount) plusOne() AttemptCount   { return AttemptCount{n: c.n + 1} }

// Visit is which time a stage is entered for a work, starting at 1.
type Visit struct{ n int }

func FirstVisit() Visit { return Visit{n: 1} }

func NewVisit(n int) (Visit, error) {
	if n < 1 {
		return Visit{}, fmt.Errorf("visit %d: %w", n, ErrInvalidVisit)
	}
	return Visit{n: n}, nil
}

func (v Visit) Int() int     { return v.n }
func (v Visit) isZero() bool { return v.n == 0 }

// Description says what a work is about.
type Description struct{ text string }

func NewDescription(s string) (Description, error) {
	t, ok := nonEmpty(s)
	if !ok {
		return Description{}, fmt.Errorf("description: %w", ErrEmptyDescription)
	}
	return Description{text: t}, nil
}

func (d Description) String() string { return d.text }
func (d Description) isZero() bool   { return d.text == "" }

// Feedback is what the owner says when rejecting a submission.
type Feedback struct{ text string }

func NewFeedback(s string) (Feedback, error) {
	t, ok := nonEmpty(s)
	if !ok {
		return Feedback{}, fmt.Errorf("feedback: %w", ErrEmptyFeedback)
	}
	return Feedback{text: t}, nil
}

func (f Feedback) String() string { return f.text }
func (f Feedback) isZero() bool   { return f.text == "" }

// Reason explains a failure, a cancellation or an abandonment.
type Reason struct{ text string }

func NewReason(s string) (Reason, error) {
	t, ok := nonEmpty(s)
	if !ok {
		return Reason{}, fmt.Errorf("reason: %w", ErrEmptyReason)
	}
	return Reason{text: t}, nil
}

func (r Reason) String() string { return r.text }
func (r Reason) isZero() bool   { return r.text == "" }

// Owner is the human who owns the workflow. Only an Owner can approve,
// reject, extend a budget or abandon work.
type Owner struct {
	name  string
	email string
}

// NewOwner requires a name; the email is optional.
func NewOwner(name, email string) (Owner, error) {
	n, ok := nonEmpty(name)
	if !ok {
		return Owner{}, fmt.Errorf("owner: %w", ErrInvalidOwner)
	}
	return Owner{name: n, email: strings.TrimSpace(email)}, nil
}

func (o Owner) Name() string  { return o.name }
func (o Owner) Email() string { return o.email }
func (o Owner) isZero() bool  { return o.name == "" }

func (o Owner) String() string {
	if o.email == "" {
		return o.name
	}
	return o.name + " <" + o.email + ">"
}

// ExitCheckOutcome is the result of a stage's exit check: passed, or failed
// with a reason. The domain never runs checks; it interprets outcomes.
type ExitCheckOutcome struct {
	kind   outcomeKind
	reason Reason
}

type outcomeKind int

const (
	outcomeUnset outcomeKind = iota
	outcomePassed
	outcomeFailed
)

func ExitCheckPassed() ExitCheckOutcome { return ExitCheckOutcome{kind: outcomePassed} }

func ExitCheckFailed(reason Reason) ExitCheckOutcome {
	return ExitCheckOutcome{kind: outcomeFailed, reason: reason}
}

func (o ExitCheckOutcome) Passed() bool   { return o.kind == outcomePassed }
func (o ExitCheckOutcome) Reason() Reason { return o.reason }

func (o ExitCheckOutcome) isZero() bool {
	return o.kind == outcomeUnset || (o.kind == outcomeFailed && o.reason.isZero())
}

func nonEmpty(s string) (string, bool) {
	t := strings.TrimSpace(s)
	return t, t != ""
}

func joinNames[T fmt.Stringer](values []T) string {
	names := make([]string, 0, len(values))
	for _, v := range values {
		names = append(names, v.String())
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// required pairs a value with its name, for ErrMissingValue reporting.
type required struct {
	name  string
	value interface{ isZero() bool }
}

func requirePresent(values ...required) error {
	for _, r := range values {
		if r.value.isZero() {
			return fmt.Errorf("%s: %w", r.name, ErrMissingValue)
		}
	}
	return nil
}
