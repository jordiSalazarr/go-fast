package domain

import (
	"fmt"
	"slices"
	"strings"
)

// NFRMeasurement is one NFR's measurement.
type NFRMeasurement struct {
	NFR         NFRID
	Measurement Measurement
}

// checkTargets rejects target NFRs a work of a type can't start with: the
// type decides. Improve-performance work targets at least one NFR, each
// once; every other type targets none. With the repository's NFRs, nfrs,
// every target must be one of them (at start; a recorded start replays as
// it was recorded, so then nfrs is nil).
func (t WorkType) checkTargets(targets []NFRID, nfrs []NFR, atStart bool) error {
	if t != WorkTypeImprovePerformance {
		if len(targets) > 0 {
			return fmt.Errorf("%s work: %w", t, ErrTargetNFRsNotAccepted)
		}
		return nil
	}
	if len(targets) == 0 {
		return fmt.Errorf("%s work: %w", t, ErrTargetNFRsRequired)
	}
	for i, id := range targets {
		if id.isZero() {
			return fmt.Errorf("target nfr: %w", ErrMissingValue)
		}
		if slices.Contains(targets[:i], id) {
			return fmt.Errorf("target nfr %s given twice: %w", id, ErrInvalidNFR)
		}
		if atStart && !slices.ContainsFunc(nfrs, func(n NFR) bool { return n.id == id }) {
			return fmt.Errorf("target nfr %s: %w", id, &UnknownTargetNFRError{NFR: id, Known: nfrs})
		}
	}
	return nil
}

// UnknownTargetNFRError names a target NFR the repository doesn't have,
// and the ones it has.
type UnknownTargetNFRError struct {
	NFR   NFRID
	Known []NFR
}

func (e *UnknownTargetNFRError) Error() string {
	return fmt.Sprintf("%s: %s is not one of [%s]", ErrUnknownTargetNFR, e.NFR, e.KnownIDs())
}

// KnownIDs lists the repository's NFR ids.
func (e *UnknownTargetNFRError) KnownIDs() string {
	ids := make([]string, len(e.Known))
	for i, n := range e.Known {
		ids[i] = n.id.value
	}
	return strings.Join(ids, ", ")
}

func (e *UnknownTargetNFRError) Unwrap() error { return ErrUnknownTargetNFR }

// MandatoryNFRs are the NFRs a stress run must script: those the work
// targets, then those whose areas cover what the work changed, each once,
// in the repository's order within each group.
func MandatoryNFRs(nfrs []NFR, changed ChangedFiles, targets []NFRID) []NFR {
	var mandatory []NFR
	for _, id := range targets {
		if i := slices.IndexFunc(nfrs, func(n NFR) bool { return n.id == id }); i >= 0 {
			mandatory = append(mandatory, nfrs[i])
		}
	}
	for _, n := range Covering(nfrs, changed) {
		if !slices.ContainsFunc(mandatory, func(m NFR) bool { return m.id == n.id }) {
			mandatory = append(mandatory, n)
		}
	}
	return mandatory
}

// MandatoryStress is review's stress choice on a path that always runs
// stress testing: include, forced by the NFRs the work targets and those
// covering what it changed. Review makes no decision there.
func MandatoryStress(nfrs []NFR, changed ChangedFiles, targets []NFRID) StressChoice {
	choice := StressChoice{decision: StressInclude, reason: Reason{text: "improve-performance work always runs stress testing against its target NFRs"}}
	for _, n := range MandatoryNFRs(nfrs, changed, targets) {
		choice.forcedBy = append(choice.forcedBy, n.id)
	}
	return choice
}

// FrozenScript is a target NFR's k6 script as measure ran it for the
// baseline: its path and its content's hash. A comparison against the
// baseline only means something if both runs use the same script, so once
// the work has a baseline, no stage may change or delete it, a later
// measure visit included. A different script needs a new baseline, and so
// a new work.
type FrozenScript struct {
	nfr  NFRID
	path RepoPath
	hash ContentHash
}

func NewFrozenScript(nfr NFRID, path RepoPath, hash ContentHash) (FrozenScript, error) {
	if err := requirePresent(required{"script nfr", nfr}, required{"script path", path}, required{"script hash", hash}); err != nil {
		return FrozenScript{}, fmt.Errorf("frozen script: %w", err)
	}
	return FrozenScript{nfr: nfr, path: path, hash: hash}, nil
}

func (s FrozenScript) NFR() NFRID        { return s.nfr }
func (s FrozenScript) Path() RepoPath    { return s.path }
func (s FrozenScript) Hash() ContentHash { return s.hash }

// Unchanged reports whether the script, as it is now, is what measure ran:
// content is its content, present false when it was deleted.
func (s FrozenScript) Unchanged(content []byte, present bool) bool {
	return present && HashOf(content) == s.hash
}

// Changed tells whoever changed the script, or is about to, what to do.
func (s FrozenScript) Changed() string {
	return fmt.Sprintf("%s changed since measure recorded the baseline: restore it. "+
		"A different script needs a new baseline: abandon the work and start again with the new script.", s.path)
}

// FrozenAt finds the frozen script at p, if any.
func FrozenAt(frozen []FrozenScript, p RepoPath) (FrozenScript, bool) {
	i := slices.IndexFunc(frozen, func(s FrozenScript) bool { return s.path == p })
	if i < 0 {
		return FrozenScript{}, false
	}
	return frozen[i], true
}

// frozenBy is the script a recorded baseline froze: none (frozen false)
// when it was recorded before scripts were frozen. ok is false when the
// event names half a script, or a script other than its NFR's.
func frozenBy(work WorkID, e BaselineMeasured) (script FrozenScript, frozen, ok bool) {
	if e.Script.isZero() && e.ScriptHash.isZero() {
		return FrozenScript{}, false, true
	}
	if e.Script != StressScriptFor(work, e.NFR) || e.ScriptHash.isZero() {
		return FrozenScript{}, false, false
	}
	return FrozenScript{nfr: e.NFR, path: e.Script, hash: e.ScriptHash}, true, true
}

// RecordBaseline records what measure measured of the work's target NFRs,
// as each result judged it against its NFR's targets, and freezes the
// scripts that measured them. Every target needs a result and its script.
// The baseline is what the code did before the work changed it, so once
// the work entered optimize, measuring again records nothing. If every
// target already meets its NFR before optimize, there is nothing to
// improve: the work completes instead of entering optimize.
func (w InProgressWork) RecordBaseline(results []NFRResult, scripts []FrozenScript) ([]WorkEvent, error) {
	if w.stage != StageMeasure {
		return nil, fmt.Errorf("record baseline of work %s on %s: %w", w.id, w.stage, ErrNotMeasuring)
	}
	if _, optimized := w.visits[StageOptimize]; optimized {
		return nil, nil
	}
	var events []WorkEvent
	var measured []NFRMeasurement
	allMet := true
	for _, target := range w.targets {
		i := slices.IndexFunc(results, func(r NFRResult) bool { return r.nfr == target })
		if i < 0 {
			return nil, fmt.Errorf("record baseline of work %s: %s: %w", w.id, target, ErrBaselineIncomplete)
		}
		j := slices.IndexFunc(scripts, func(s FrozenScript) bool { return s.nfr == target })
		if j < 0 || scripts[j].path != StressScriptFor(w.id, target) {
			return nil, fmt.Errorf("record baseline of work %s: %s: no script: %w", w.id, target, ErrBaselineIncomplete)
		}
		events = append(events, BaselineMeasured{WorkID: w.id, NFR: target, Measurement: results[i].measurement,
			Script: scripts[j].path, ScriptHash: scripts[j].hash})
		measured = append(measured, NFRMeasurement{NFR: target, Measurement: results[i].measurement})
		allMet = allMet && results[i].met
	}
	if allMet {
		events = append(events, WorkFoundNothingToImprove{WorkID: w.id, Measurements: measured})
	}
	return events, nil
}

// CompareToBaseline describes how each target the NFR sets moved since the
// baseline, e.g. "p95 212ms → 120ms (target 150ms)". It is for information:
// the targets alone decide whether the NFR is met.
func CompareToBaseline(n NFR, baseline, now Measurement) string {
	var parts []string
	t := n.targets.v
	if t.P95 != nil {
		parts = append(parts, fmt.Sprintf("p95 %s → %s (target %s)", formatLatency(baseline.p95), formatLatency(now.p95), formatLatency(*t.P95)))
	}
	if t.P99 != nil {
		parts = append(parts, fmt.Sprintf("p99 %s → %s (target %s)", formatLatency(baseline.p99), formatLatency(now.p99), formatLatency(*t.P99)))
	}
	if t.MaxErrorRate != nil {
		parts = append(parts, fmt.Sprintf("error rate %s → %s (target %s)", formatRate(baseline.errorRate), formatRate(now.errorRate), formatRate(*t.MaxErrorRate)))
	}
	if t.MinRPS != nil {
		parts = append(parts, fmt.Sprintf("rps %s → %s (target %s)", formatRPS(baseline.rps), formatRPS(now.rps), formatRPS(*t.MinRPS)))
	}
	return strings.Join(parts, ", ")
}
