package domain

import "strings"

// AgentClaim is what the stage agent reports about its stage's exit check:
// passed, or failed with a reason.
type AgentClaim struct {
	passed bool
	reason Reason
}

func ClaimPassed() AgentClaim { return AgentClaim{passed: true} }

func ClaimFailed(reason Reason) AgentClaim { return AgentClaim{reason: reason} }

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

// ExitCheckOutcome is the result of a stage's exit check: the agent's claim
// together with the scope check. Files changed outside the write scope make
// the attempt fail, whatever the agent claims. The domain never runs checks;
// it interprets their results.
type ExitCheckOutcome struct {
	passed bool
	reason Reason
}

func NewExitCheckOutcome(claim AgentClaim, scope ScopeCheck) ExitCheckOutcome {
	switch {
	case scope.passed():
		return ExitCheckOutcome{passed: claim.passed, reason: claim.reason}
	case claim.passed:
		return ExitCheckOutcome{reason: Reason{text: scope.reason()}}
	default:
		return ExitCheckOutcome{reason: Reason{text: sentence(claim.reason.text) + " " + scope.reason()}}
	}
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
