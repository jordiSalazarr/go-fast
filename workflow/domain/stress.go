package domain

import (
	"fmt"
	"slices"
)

// StressDecision is whether stress testing runs for a work.
type StressDecision struct{ name string }

var (
	StressInclude = StressDecision{name: "include"}
	StressSkip    = StressDecision{name: "skip"}
)

func ParseStressDecision(s string) (StressDecision, error) {
	for _, d := range []StressDecision{StressInclude, StressSkip} {
		if d.name == s {
			return d, nil
		}
	}
	return StressDecision{}, fmt.Errorf("stress decision %q (available: include, skip): %w", s, ErrUnknownStressDecision)
}

func (d StressDecision) String() string { return d.name }
func (d StressDecision) isZero() bool   { return d.name == "" }

// StressChoice is a stress decision with its reason, and the NFRs that
// forced it: those covering the files the work changed. A forced choice is
// always include.
type StressChoice struct {
	decision StressDecision
	reason   Reason
	forcedBy []NFRID
}

// ChooseStress decides stress testing for a work that changed these files,
// given the repository's NFRs. If any NFR covers the change, stress testing
// is mandatory: skip is rejected with a *StressForcedError naming the NFRs.
func ChooseStress(decision StressDecision, reason Reason, nfrs []NFR, changed ChangedFiles) (StressChoice, error) {
	if err := requirePresent(required{"stress decision", decision}, required{"reason", reason}); err != nil {
		return StressChoice{}, fmt.Errorf("choose stress testing: %w", err)
	}
	covering := Covering(nfrs, changed)
	if decision == StressSkip && len(covering) > 0 {
		return StressChoice{}, &StressForcedError{NFRs: covering}
	}
	choice := StressChoice{decision: decision, reason: reason}
	for _, n := range covering {
		choice.forcedBy = append(choice.forcedBy, n.id)
	}
	return choice, nil
}

// NewStressChoice restores a recorded choice. A skip can't have been forced.
func NewStressChoice(decision StressDecision, reason Reason, forcedBy []NFRID) (StressChoice, error) {
	if err := requirePresent(required{"stress decision", decision}, required{"reason", reason}); err != nil {
		return StressChoice{}, fmt.Errorf("stress choice: %w", err)
	}
	if decision == StressSkip && len(forcedBy) > 0 {
		return StressChoice{}, fmt.Errorf("stress choice: skip forced by %d NFRs: %w", len(forcedBy), ErrStressForced)
	}
	return StressChoice{decision: decision, reason: reason, forcedBy: slices.Clone(forcedBy)}, nil
}

func (c StressChoice) Decision() StressDecision { return c.decision }
func (c StressChoice) Reason() Reason           { return c.reason }

// ForcedBy are the NFRs that made stress testing mandatory; none if it wasn't.
func (c StressChoice) ForcedBy() []NFRID { return slices.Clone(c.forcedBy) }
func (c StressChoice) isZero() bool      { return c.decision.isZero() }

func (c StressChoice) valid() bool {
	return !c.decision.isZero() && !c.reason.isZero() && !(c.decision == StressSkip && len(c.forcedBy) > 0)
}

// stressDecision is the work's recorded decision for its current pass along
// the path; a rework clears it. owner is zero when review decided.
type stressDecision struct {
	choice StressChoice
	owner  Owner
}

func decidedBy(e StressDecided) *stressDecision {
	return &stressDecision{choice: StressChoice{decision: e.Decision, reason: e.Reason, forcedBy: e.ForcedBy}, owner: e.Owner}
}

// StressForcedError names the NFRs that cover the work's changes, which make
// stress testing mandatory.
type StressForcedError struct {
	NFRs []NFR
}

func (e *StressForcedError) Error() string {
	return fmt.Sprintf("%s: %s", ErrStressForced, e.Describe())
}

// Describe names each NFR and its areas, e.g. "wallet-transaction-latency
// (internal/wallet/**, internal/ledger/**)".
func (e *StressForcedError) Describe() string {
	var out string
	for i, n := range e.NFRs {
		if i > 0 {
			out += "; "
		}
		out += n.id.value + " ("
		for j, a := range n.areas {
			if j > 0 {
				out += ", "
			}
			out += a.pattern
		}
		out += ")"
	}
	return out
}

func (e *StressForcedError) Unwrap() error { return ErrStressForced }
