package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
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
	// StageFixer is on no path: a work enters it only when a stage that
	// checks code hands it over, and leaves it for an earlier stage.
	StageFixer = Stage{name: "fixer"}
)

func Stages() []Stage {
	return []Stage{StageDiscovery, StageSpecify, StageImplement, StageReview, StageIntegrationTesting, StageFixer}
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

// ReworkBudget is a positive number of times a work may enter the fixer.
type ReworkBudget struct{ n int }

// DefaultReworkBudget is every work's rework budget until the owner extends it.
func DefaultReworkBudget() ReworkBudget { return ReworkBudget{n: 3} }

func NewReworkBudget(n int) (ReworkBudget, error) {
	if n < 1 {
		return ReworkBudget{}, fmt.Errorf("rework budget %d: %w", n, ErrInvalidReworkBudget)
	}
	return ReworkBudget{n: n}, nil
}

func (b ReworkBudget) Int() int     { return b.n }
func (b ReworkBudget) isZero() bool { return b.n == 0 }

func (b ReworkBudget) exhaustedBy(used ReworkCount) bool { return used.n >= b.n }

func (b ReworkBudget) extendedBy(additional ReworkBudget) ReworkBudget {
	return ReworkBudget{n: b.n + additional.n}
}

// ReworkCount is how many times a work entered the fixer.
type ReworkCount struct{ n int }

func NewReworkCount(n int) (ReworkCount, error) {
	if n < 0 {
		return ReworkCount{}, fmt.Errorf("rework count %d: %w", n, ErrInvalidReworkBudget)
	}
	return ReworkCount{n: n}, nil
}

func (c ReworkCount) Int() int { return c.n }

func (c ReworkCount) plusOne() ReworkCount { return ReworkCount{n: c.n + 1} }

// FixerMode is what the fixer looks for, given the stage that handed over.
type FixerMode struct{ name string }

var (
	// FixerModeCorrectness: the code does the wrong thing (from review or
	// integration testing).
	FixerModeCorrectness = FixerMode{name: "correctness"}
	// FixerModePerformance: the code is too slow (from stress testing, which
	// doesn't exist yet).
	FixerModePerformance = FixerMode{name: "performance"}
)

func ParseFixerMode(s string) (FixerMode, error) {
	for _, m := range []FixerMode{FixerModeCorrectness, FixerModePerformance} {
		if m.name == s {
			return m, nil
		}
	}
	return FixerMode{}, fmt.Errorf("fixer mode %q (available: correctness, performance): %w", s, ErrUnknownFixerMode)
}

func (m FixerMode) String() string { return m.name }

// fixerModeFor is the mode of the fixer entered from a stage. Every stage
// that can hand over today judges correctness.
func fixerModeFor(Stage) FixerMode { return FixerModeCorrectness }

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

func (v Visit) next() Visit { return Visit{n: v.n + 1} }

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

// Branch is the git branch a work belongs to.
type Branch struct{ name string }

func NewBranch(s string) (Branch, error) {
	if s == "" || strings.ContainsFunc(s, unicode.IsSpace) {
		return Branch{}, fmt.Errorf("branch %q: %w", s, ErrInvalidBranch)
	}
	return Branch{name: s}, nil
}

func (b Branch) String() string { return b.name }
func (b Branch) isZero() bool   { return b.name == "" }

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
