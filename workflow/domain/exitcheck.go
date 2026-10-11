package domain

import (
	"fmt"
	"slices"
	"strings"
)

// ExitCheck is one named check a stage's exit can require. gf runs the
// checks; the domain says which ones a stage requires and interprets their
// results.
type ExitCheck struct{ name string }

var (
	// CheckArtifactWritten: the stage artifact exists, is not empty, and
	// changed during this stage visit.
	CheckArtifactWritten = ExitCheck{name: "artifact-written"}
	// CheckSpecsFail: the tests written in this stage fail for a behavioural
	// reason, not a build failure.
	CheckSpecsFail = ExitCheck{name: "specs-fail"}
	// CheckBuildPasses: the code builds and vets.
	CheckBuildPasses = ExitCheck{name: "build-passes"}
	// CheckSpecsPass: the tests written during specify pass.
	CheckSpecsPass = ExitCheck{name: "specs-pass"}
	// CheckSpecsUntouched: the tests written during specify were neither
	// modified nor deleted.
	CheckSpecsUntouched = ExitCheck{name: "specs-untouched"}
	// CheckSuitePasses: the whole test suite passes.
	CheckSuitePasses = ExitCheck{name: "suite-passes"}
	// CheckFormatted: the code is formatted.
	CheckFormatted = ExitCheck{name: "formatted"}
	// CheckIntegrationPasses: the repository's integration command passes.
	CheckIntegrationPasses = ExitCheck{name: "integration-passes"}
)

// ExitChecks lists every exit check.
func ExitChecks() []ExitCheck {
	return []ExitCheck{
		CheckArtifactWritten, CheckSpecsFail, CheckBuildPasses, CheckSpecsPass,
		CheckSpecsUntouched, CheckSuitePasses, CheckFormatted, CheckIntegrationPasses,
	}
}

func (c ExitCheck) String() string { return c.name }
func (c ExitCheck) isZero() bool   { return c.name == "" }

// RequiredChecks are the exit checks a stage visit must pass, in the order
// their failures are reported.
func (s Stage) RequiredChecks() []ExitCheck {
	switch s {
	case StageDiscovery:
		return []ExitCheck{CheckArtifactWritten}
	case StageSpecify:
		return []ExitCheck{CheckArtifactWritten, CheckSpecsFail}
	case StageImplement:
		return []ExitCheck{CheckArtifactWritten, CheckBuildPasses, CheckFormatted, CheckSpecsPass, CheckSpecsUntouched, CheckSuitePasses}
	case StageReview:
		return []ExitCheck{CheckArtifactWritten, CheckFormatted, CheckBuildPasses, CheckSuitePasses}
	case StageIntegrationTesting:
		return []ExitCheck{CheckArtifactWritten, CheckIntegrationPasses}
	case StageFixer:
		return []ExitCheck{CheckArtifactWritten}
	}
	return nil
}

// CheckResult is how one exit check went: passed, or failed with a short
// summary of why.
type CheckResult struct {
	check   ExitCheck
	passed  bool
	summary string
}

func CheckPassed(check ExitCheck) CheckResult { return CheckResult{check: check, passed: true} }

// CheckFailed requires a summary: a failure always says why.
func CheckFailed(check ExitCheck, summary string) (CheckResult, error) {
	if err := requirePresent(required{"exit check", check}); err != nil {
		return CheckResult{}, fmt.Errorf("check result: %w", err)
	}
	s, ok := nonEmpty(summary)
	if !ok {
		return CheckResult{}, fmt.Errorf("check result of %s: %w", check, ErrEmptyCheckSummary)
	}
	return CheckResult{check: check, summary: s}, nil
}

func (r CheckResult) Check() ExitCheck { return r.check }
func (r CheckResult) Passed() bool     { return r.passed }
func (r CheckResult) Summary() string  { return r.summary }

// AgentClaim is what the stage agent reports about its stage's exit check:
// passed, or failed with a reason. The fixer's pass also carries its route.
type AgentClaim struct {
	passed bool
	reason Reason
	route  Route
}

func ClaimPassed() AgentClaim { return AgentClaim{passed: true} }

// ClaimRouted is the fixer's pass: the work goes to the route's target.
func ClaimRouted(route Route) AgentClaim { return AgentClaim{passed: true, route: route} }

func ClaimFailed(reason Reason) AgentClaim { return AgentClaim{reason: reason} }

func (c AgentClaim) Passed() bool { return c.passed }

// Route is the fixer's route; ok is false for any other claim.
func (c AgentClaim) Route() (route Route, ok bool) { return c.route, !c.route.isZero() }

// Route is where the fixer sends the work, and why.
type Route struct {
	target Stage
	reason Reason
}

// NewRoute needs both a target and a reason. Whether the target is allowed
// depends on the work: see InFixerWork.CheckRoute.
func NewRoute(target Stage, reason Reason) (Route, error) {
	if err := requirePresent(required{"route target", target}, required{"route reason", reason}); err != nil {
		return Route{}, fmt.Errorf("route: %w", err)
	}
	return Route{target: target, reason: reason}, nil
}

func (r Route) Target() Stage  { return r.target }
func (r Route) Reason() Reason { return r.reason }
func (r Route) isZero() bool   { return r.target.isZero() }

func (c AgentClaim) isZero() bool { return !c.passed && c.reason.isZero() }

// ScopeCheck is the files a stage visit changed, checked against its stage's
// write scope.
type ScopeCheck struct {
	stage      Stage
	violations []RepoPath
}

// CheckWriteScope checks the files changed during a stage visit of work.
func CheckWriteScope(stage Stage, work WorkID, changed ChangedFiles) ScopeCheck {
	return ScopeCheck{stage: stage, violations: stage.WriteScope().Violations(changed, work)}
}

func (c ScopeCheck) Violations() []RepoPath { return append([]RepoPath(nil), c.violations...) }

func (c ScopeCheck) passed() bool { return len(c.violations) == 0 }

func (c ScopeCheck) reason() string {
	names := make([]string, len(c.violations))
	for i, p := range c.violations {
		names[i] = p.value
	}
	return "Changed files outside the '" + c.stage.name + "' write scope: " + strings.Join(names, ", ") + "."
}

// ExitCheckOutcome is the result of a stage's exit: the agent's claim, the
// scope check, and the results of the exit checks the stage requires. It
// passes only if all three do. A failed claim is the agent's own verdict, so
// no checks are needed then. The domain never runs checks; it interprets
// their results.
type ExitCheckOutcome struct {
	stage  Stage
	passed bool
	reason Reason
	// What failed, for ClassifyFailure.
	scopeViolated bool
	failedChecks  []ExitCheck
}

// NewExitCheckOutcome rejects a passing claim whose results lack a check the
// stage requires, so a check can never be skipped silently. The reason of a
// failed outcome lists every failure: the claim, then the scope, then the
// checks in the order the stage requires them.
func NewExitCheckOutcome(claim AgentClaim, scope ScopeCheck, results []CheckResult) (ExitCheckOutcome, error) {
	var failures []string
	var failedChecks []ExitCheck
	if !claim.passed {
		failures = append(failures, sentence(claim.reason.text))
	}
	if !scope.passed() {
		failures = append(failures, scope.reason())
	}
	if claim.passed {
		var failed []string
		var err error
		failedChecks, failed, err = failingChecks(scope.stage, results)
		if err != nil {
			return ExitCheckOutcome{}, err
		}
		if len(failed) > 0 {
			failures = append(failures, sentence(strings.Join(failed, "; ")))
		}
	}
	if len(failures) == 0 {
		return ExitCheckOutcome{stage: scope.stage, passed: true}, nil
	}
	outcome := ExitCheckOutcome{stage: scope.stage, scopeViolated: !scope.passed(), failedChecks: failedChecks}
	if !claim.passed && scope.passed() {
		outcome.reason = claim.reason // the agent's words, as given
		return outcome, nil
	}
	outcome.reason = Reason{text: strings.Join(failures, " ")}
	return outcome, nil
}

// failingChecks returns the stage's required checks that failed, and a
// description of each, in the stage's order. Results of checks the stage
// does not require are ignored.
func failingChecks(stage Stage, results []CheckResult) ([]ExitCheck, []string, error) {
	var checks []ExitCheck
	var described []string
	for _, check := range stage.RequiredChecks() {
		result, ok := resultOf(check, results)
		if !ok {
			return nil, nil, fmt.Errorf("exit check %s of stage %s has no result: %w", check, stage, ErrMissingValue)
		}
		if !result.passed {
			checks = append(checks, check)
			described = append(described, check.name+": "+strings.TrimSuffix(result.summary, "."))
		}
	}
	return checks, described, nil
}

func resultOf(check ExitCheck, results []CheckResult) (CheckResult, bool) {
	for _, r := range results {
		if r.check == check {
			return r, true
		}
	}
	return CheckResult{}, false
}

func (o ExitCheckOutcome) Passed() bool   { return o.passed }
func (o ExitCheckOutcome) Reason() Reason { return o.reason }

// FailureKind says why a stage visit's exit failed, which decides what
// happens next.
type FailureKind struct{ name string }

var (
	// StageWorkFailure: the agent didn't do its own stage properly. The same
	// stage tries again, using an attempt.
	StageWorkFailure = FailureKind{name: "stage-work"}
	// CodeFailure: the code is wrong, and the stage can't change code. The
	// work goes to the fixer.
	CodeFailure = FailureKind{name: "code"}
)

func (k FailureKind) String() string { return k.name }

// ClassifyFailure says why a failed outcome failed; ok is false for an
// outcome that passed. It depends only on what failed and on the stage the
// outcome is about: its write scope, and whether it checks code.
//
// A missing or unchanged artifact, or a change outside the write scope, is
// always the stage's own work. So is any failure of a stage that can change
// code: it fixes the code itself on its next attempt. A stage that checks
// code it can't change (review, integration testing) fails on the code when
// one of its other checks fails, or when its agent says the code is wrong.
func ClassifyFailure(outcome ExitCheckOutcome) (kind FailureKind, ok bool) {
	if outcome.passed {
		return FailureKind{}, false
	}
	if outcome.scopeViolated || slices.Contains(outcome.failedChecks, CheckArtifactWritten) || !outcome.stage.ChecksCode() {
		return StageWorkFailure, true
	}
	return CodeFailure, true
}

// ChecksCode reports whether the stage judges code it can't change: its
// write scope can't change code, yet it requires a check other than
// artifact-written. Discovery and the fixer only write their artifact, so
// their failures are always their own work.
func (s Stage) ChecksCode() bool {
	return !s.WriteScope().CanChangeCode() && slices.ContainsFunc(s.RequiredChecks(), func(c ExitCheck) bool {
		return c != CheckArtifactWritten
	})
}

// sentence ends s with a full stop unless it already ends a sentence.
func sentence(s string) string {
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}
