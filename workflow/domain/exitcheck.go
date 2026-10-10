package domain

import (
	"fmt"
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
		return []ExitCheck{CheckArtifactWritten, CheckBuildPasses, CheckSpecsPass, CheckSpecsUntouched, CheckSuitePasses}
	case StageReview:
		return []ExitCheck{CheckArtifactWritten, CheckFormatted, CheckBuildPasses, CheckSuitePasses}
	case StageIntegrationTesting:
		return []ExitCheck{CheckArtifactWritten, CheckIntegrationPasses}
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
// passed, or failed with a reason.
type AgentClaim struct {
	passed bool
	reason Reason
}

func ClaimPassed() AgentClaim { return AgentClaim{passed: true} }

func ClaimFailed(reason Reason) AgentClaim { return AgentClaim{reason: reason} }

func (c AgentClaim) Passed() bool { return c.passed }

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
	passed bool
	reason Reason
}

// NewExitCheckOutcome rejects a passing claim whose results lack a check the
// stage requires, so a check can never be skipped silently. The reason of a
// failed outcome lists every failure: the claim, then the scope, then the
// checks in the order the stage requires them.
func NewExitCheckOutcome(claim AgentClaim, scope ScopeCheck, results []CheckResult) (ExitCheckOutcome, error) {
	var failures []string
	if !claim.passed {
		failures = append(failures, sentence(claim.reason.text))
	}
	if !scope.passed() {
		failures = append(failures, scope.reason())
	}
	if claim.passed {
		failed, err := failedChecks(scope.stage, results)
		if err != nil {
			return ExitCheckOutcome{}, err
		}
		if len(failed) > 0 {
			failures = append(failures, sentence(strings.Join(failed, "; ")))
		}
	}
	if len(failures) == 0 {
		return ExitCheckOutcome{passed: true}, nil
	}
	if !claim.passed && scope.passed() {
		return ExitCheckOutcome{reason: claim.reason}, nil // the agent's words, as given
	}
	return ExitCheckOutcome{reason: Reason{text: strings.Join(failures, " ")}}, nil
}

// failedChecks describes the stage's required checks that failed, in the
// stage's order. Results of checks the stage does not require are ignored.
func failedChecks(stage Stage, results []CheckResult) ([]string, error) {
	var failed []string
	for _, check := range stage.RequiredChecks() {
		result, ok := resultOf(check, results)
		if !ok {
			return nil, fmt.Errorf("exit check %s of stage %s has no result: %w", check, stage, ErrMissingValue)
		}
		if !result.passed {
			failed = append(failed, check.name+": "+strings.TrimSuffix(result.summary, "."))
		}
	}
	return failed, nil
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

// sentence ends s with a full stop unless it already ends a sentence.
func sentence(s string) string {
	if strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "."
}
