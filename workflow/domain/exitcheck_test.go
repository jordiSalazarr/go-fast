package domain_test

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func TestEachStageRequiresItsExitChecks(t *testing.T) {
	want := map[domain.Stage][]domain.ExitCheck{
		domain.StageDiscovery: {domain.CheckArtifactWritten},
		domain.StageSpecify:   {domain.CheckArtifactWritten, domain.CheckSpecsFail},
		domain.StageImplement: {domain.CheckArtifactWritten, domain.CheckBuildPasses, domain.CheckFormatted, domain.CheckSpecsPass,
			domain.CheckSpecsUntouched, domain.CheckSuitePasses},
		domain.StageReview:             {domain.CheckArtifactWritten, domain.CheckFormatted, domain.CheckBuildPasses, domain.CheckSuitePasses},
		domain.StageIntegrationTesting: {domain.CheckArtifactWritten, domain.CheckIntegrationPasses},
		domain.StageStressTesting:      {domain.CheckArtifactWritten, domain.CheckStressScriptsValid, domain.CheckNFRsMet},
		domain.StageFixer:              {domain.CheckArtifactWritten},
	}
	for _, stage := range domain.Stages() {
		assert.Equal(t, want[stage], stage.RequiredChecks(), stage.String())
	}
}

func TestExitChecksHaveStableNames(t *testing.T) {
	var names []string
	for _, c := range domain.ExitChecks() {
		names = append(names, c.String())
	}
	assert.Equal(t, []string{"artifact-written", "specs-fail", "build-passes", "specs-pass",
		"specs-untouched", "suite-passes", "formatted", "integration-passes", "stress-scripts-valid", "nfrs-met"}, names)
}

func TestCheckFailed_NeedsASummary(t *testing.T) {
	_, err := domain.CheckFailed(domain.CheckFormatted, "  ")
	require.ErrorIs(t, err, domain.ErrEmptyCheckSummary)

	_, err = domain.CheckFailed(domain.ExitCheck{}, "x")
	require.ErrorIs(t, err, domain.ErrMissingValue)

	r := must(domain.CheckFailed(domain.CheckFormatted, " main.go is not formatted "))
	assert.False(t, r.Passed())
	assert.Equal(t, "main.go is not formatted", r.Summary())
}

// Given a claim, a scope check and check results, when the outcome is built,
// then it passes or fails with this reason.

var (
	inScope      = domain.CheckWriteScope(domain.StageSpecify, workID, domain.NewChangedFiles(must(domain.NewRepoPath("/repo", "a_test.go"))))
	outOfScope   = domain.CheckWriteScope(domain.StageSpecify, workID, domain.NewChangedFiles(must(domain.NewRepoPath("/repo", "a.go"))))
	specifyPass  = []domain.CheckResult{domain.CheckPassed(domain.CheckArtifactWritten), domain.CheckPassed(domain.CheckSpecsFail)}
	specsNotFail = []domain.CheckResult{
		domain.CheckPassed(domain.CheckArtifactWritten),
		checkFailed(domain.CheckSpecsFail, "no test written in this stage fails"),
	}
)

func TestExitCheckOutcome(t *testing.T) {
	cases := []struct {
		name    string
		claim   domain.AgentClaim
		scope   domain.ScopeCheck
		results []domain.CheckResult
		passed  bool
		reason  string
	}{
		{"passed, in scope, checks pass", domain.ClaimPassed(), inScope, specifyPass, true, ""},
		{"passed, in scope, a check fails", domain.ClaimPassed(), inScope, specsNotFail, false,
			"specs-fail: no test written in this stage fails."},
		{"passed, out of scope, checks pass", domain.ClaimPassed(), outOfScope, specifyPass, false,
			"Changed files outside the 'specify' write scope: a.go."},
		{"passed, out of scope, a check fails", domain.ClaimPassed(), outOfScope, specsNotFail, false,
			"Changed files outside the 'specify' write scope: a.go. specs-fail: no test written in this stage fails."},
		{"failed, in scope", domain.ClaimFailed(reasonOf("tests fail")), inScope, nil, false, "tests fail"},
		{"failed, out of scope", domain.ClaimFailed(reasonOf("tests fail")), outOfScope, nil, false,
			"tests fail. Changed files outside the 'specify' write scope: a.go."},
		{"failed sentence, out of scope", domain.ClaimFailed(reasonOf("Tests fail!")), outOfScope, nil, false,
			"Tests fail! Changed files outside the 'specify' write scope: a.go."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outcome, err := domain.NewExitCheckOutcome(c.claim, c.scope, c.results)
			require.NoError(t, err)
			assert.Equal(t, c.passed, outcome.Passed())
			assert.Equal(t, c.reason, outcome.Reason().String())
		})
	}
}

func TestExitCheckOutcome_GivenFailingChecks_ThenTheReasonListsThemInTheStageOrder(t *testing.T) {
	implement := domain.CheckWriteScope(domain.StageImplement, workID, domain.ChangedFiles{})
	results := []domain.CheckResult{ // in a different order than the stage requires them
		checkFailed(domain.CheckSuitePasses, "2 tests failed (TestServer_Listens, TestConfig_Port)."),
		domain.CheckPassed(domain.CheckSpecsPass),
		checkFailed(domain.CheckSpecsUntouched, "server_test.go was modified"),
		domain.CheckPassed(domain.CheckBuildPasses),
		domain.CheckPassed(domain.CheckFormatted),
		checkFailed(domain.CheckArtifactWritten, ".gofast/works/w1/implement-v1.md is missing"),
		checkFailed(domain.CheckIntegrationPasses, "not required on implement, so ignored"),
	}

	outcome, err := domain.NewExitCheckOutcome(domain.ClaimPassed(), implement, results)

	require.NoError(t, err)
	assert.False(t, outcome.Passed())
	assert.Equal(t, "artifact-written: .gofast/works/w1/implement-v1.md is missing; "+
		"specs-untouched: server_test.go was modified; "+
		"suite-passes: 2 tests failed (TestServer_Listens, TestConfig_Port).", outcome.Reason().String())
}

func TestExitCheckOutcome_GivenAPassingClaim_WhenARequiredResultIsMissing_ThenItIsAnError(t *testing.T) {
	for _, results := range [][]domain.CheckResult{
		nil,
		{domain.CheckPassed(domain.CheckArtifactWritten)},
		{domain.CheckPassed(domain.CheckArtifactWritten), domain.CheckPassed(domain.CheckBuildPasses)},
	} {
		_, err := domain.NewExitCheckOutcome(domain.ClaimPassed(), inScope, results)
		require.ErrorIs(t, err, domain.ErrMissingValue)
	}
}

func TestExitCheckOutcome_GivenAFailedClaim_ThenNoCheckResultsAreNeeded(t *testing.T) {
	outcome, err := domain.NewExitCheckOutcome(domain.ClaimFailed(reasonOf("cannot reproduce")), inScope, nil)

	require.NoError(t, err)
	assert.False(t, outcome.Passed())
	assert.Equal(t, "cannot reproduce", outcome.Reason().String())
}

func TestExitCheckOutcome_GivenAFailedClaim_ThenCheckResultsAreIgnored(t *testing.T) {
	outcome, err := domain.NewExitCheckOutcome(domain.ClaimFailed(reasonOf("cannot reproduce")), inScope, specsNotFail)

	require.NoError(t, err)
	assert.Equal(t, "cannot reproduce", outcome.Reason().String())
}

// A stage that can't change code can only fail a check about code until it
// escalates, so an earlier stage on the path that can change code must
// require that check too. These checks are exempt:
//   - artifact-written and stress-scripts-valid are about what the stage
//     itself writes (its artifact, its k6 scripts), not about code;
//   - integration-passes and nfrs-met are integration and stress testing's
//     own purposes: a failure there is a finding that needs rework, not a
//     check implement forgot.
func TestEveryCheckAboutCode_IsRequiredBeforeTheStagesThatCantChangeCode(t *testing.T) {
	exempt := map[domain.ExitCheck]bool{
		domain.CheckArtifactWritten: true, domain.CheckStressScriptsValid: true,
		domain.CheckIntegrationPasses: true, domain.CheckNFRsMet: true,
	}
	// Code is any Go file, tests included: specify writes test code.
	code := []domain.RepoPath{must(domain.NewRepoPath("/repo", "server/server.go")), must(domain.NewRepoPath("/repo", "server/server_test.go"))}
	canChangeCode := func(stage domain.Stage) bool {
		return slices.ContainsFunc(code, func(p domain.RepoPath) bool { return stage.WriteScope().Allows(p, workID) })
	}
	for _, workType := range domain.WorkTypes() {
		var requiredByCodeStages []domain.ExitCheck
		// The fixer is on no path, but is entered only after every stage
		// that can hand over, so it comes last.
		for _, step := range append(must(domain.PathFor(workType)).Steps(), domain.FixerStep()) {
			stage := step.Stage()
			if canChangeCode(stage) {
				requiredByCodeStages = append(requiredByCodeStages, stage.RequiredChecks()...)
				continue
			}
			for _, check := range stage.RequiredChecks() {
				if !exempt[check] {
					assert.Containsf(t, requiredByCodeStages, check,
						"%s: '%s' can't change code but requires %s, which no earlier stage that can change code requires",
						workType, stage, check)
				}
			}
		}
	}
}

// Given a stage's exit outcome, when it is classified, then a failure is the
// stage's own work (retry it) or the code's (hand it to the fixer).
func TestClassifyFailure(t *testing.T) {
	scopeOf := func(stage domain.Stage, paths ...string) domain.ScopeCheck {
		var changed []domain.RepoPath
		for _, p := range paths {
			changed = append(changed, must(domain.NewRepoPath("/repo", p)))
		}
		return domain.CheckWriteScope(stage, workID, domain.NewChangedFiles(changed...))
	}
	passedAll := func(stage domain.Stage, failing ...domain.CheckResult) []domain.CheckResult {
		var results []domain.CheckResult
		for _, c := range stage.RequiredChecks() {
			results = append(results, domain.CheckPassed(c))
		}
		return append(failing, results...) // a failing result is found first
	}
	artifactMissing := checkFailed(domain.CheckArtifactWritten, "the artifact is missing")
	cases := []struct {
		name    string
		stage   domain.Stage
		claim   domain.AgentClaim
		scope   domain.ScopeCheck
		results []domain.CheckResult
		want    domain.FailureKind
	}{
		{"review: artifact not written", domain.StageReview, domain.ClaimPassed(), scopeOf(domain.StageReview),
			passedAll(domain.StageReview, artifactMissing), domain.StageWorkFailure},
		{"review: artifact not written and suite fails", domain.StageReview, domain.ClaimPassed(), scopeOf(domain.StageReview),
			passedAll(domain.StageReview, artifactMissing, checkFailed(domain.CheckSuitePasses, "1 test failed")), domain.StageWorkFailure},
		{"review: wrote outside its scope", domain.StageReview, domain.ClaimPassed(), scopeOf(domain.StageReview, "server/server.go"),
			passedAll(domain.StageReview), domain.StageWorkFailure},
		{"review: wrote outside its scope and claims a failure", domain.StageReview, domain.ClaimFailed(reasonOf("blocking finding")),
			scopeOf(domain.StageReview, "server/server.go"), nil, domain.StageWorkFailure},
		{"review: suite fails", domain.StageReview, domain.ClaimPassed(), scopeOf(domain.StageReview),
			passedAll(domain.StageReview, checkFailed(domain.CheckSuitePasses, "1 test failed")), domain.CodeFailure},
		{"review: agent found a blocking problem", domain.StageReview, domain.ClaimFailed(reasonOf("blocking finding")),
			scopeOf(domain.StageReview), nil, domain.CodeFailure},
		{"integration testing: integration fails", domain.StageIntegrationTesting, domain.ClaimPassed(), scopeOf(domain.StageIntegrationTesting),
			passedAll(domain.StageIntegrationTesting, checkFailed(domain.CheckIntegrationPasses, "exit status 1")), domain.CodeFailure},
		{"integration testing: agent claims a failure", domain.StageIntegrationTesting, domain.ClaimFailed(reasonOf("flaky endpoint")),
			scopeOf(domain.StageIntegrationTesting), nil, domain.CodeFailure},
		{"stress testing: an NFR missed its targets", domain.StageStressTesting, domain.ClaimPassed(), scopeOf(domain.StageStressTesting),
			passedAll(domain.StageStressTesting, checkFailed(domain.CheckNFRsMet, "wallet: p95 212ms > 150ms")), domain.CodeFailure},
		{"stress testing: a script sets thresholds", domain.StageStressTesting, domain.ClaimPassed(), scopeOf(domain.StageStressTesting),
			passedAll(domain.StageStressTesting, checkFailed(domain.CheckStressScriptsValid, "wallet.js sets options.thresholds")), domain.StageWorkFailure},
		{"stress testing: invalid scripts and missed targets", domain.StageStressTesting, domain.ClaimPassed(), scopeOf(domain.StageStressTesting),
			passedAll(domain.StageStressTesting, checkFailed(domain.CheckStressScriptsValid, "no script for wallet"),
				checkFailed(domain.CheckNFRsMet, "not measured")), domain.StageWorkFailure},
		{"stress testing: artifact not written", domain.StageStressTesting, domain.ClaimPassed(), scopeOf(domain.StageStressTesting),
			passedAll(domain.StageStressTesting, artifactMissing), domain.StageWorkFailure},
		{"stress testing: wrote outside its scope", domain.StageStressTesting, domain.ClaimPassed(), scopeOf(domain.StageStressTesting, "server/server.go"),
			passedAll(domain.StageStressTesting), domain.StageWorkFailure},
		{"implement: suite fails", domain.StageImplement, domain.ClaimPassed(), scopeOf(domain.StageImplement),
			passedAll(domain.StageImplement, checkFailed(domain.CheckSuitePasses, "1 test failed")), domain.StageWorkFailure},
		{"implement: agent claims a failure", domain.StageImplement, domain.ClaimFailed(reasonOf("can't fix it")),
			scopeOf(domain.StageImplement), nil, domain.StageWorkFailure},
		{"specify: agent claims a failure", domain.StageSpecify, domain.ClaimFailed(reasonOf("can't reproduce")),
			scopeOf(domain.StageSpecify), nil, domain.StageWorkFailure},
		{"discovery: agent claims a failure", domain.StageDiscovery, domain.ClaimFailed(reasonOf("can't reproduce")),
			scopeOf(domain.StageDiscovery), nil, domain.StageWorkFailure},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outcome, err := domain.NewExitCheckOutcome(c.claim, c.scope, c.results)
			require.NoError(t, err)

			kind, failed := domain.ClassifyFailure(outcome)

			require.True(t, failed)
			assert.Equal(t, c.want, kind)
		})
	}
}

func TestClassifyFailure_GivenAPassingOutcome_ThenThereIsNoFailure(t *testing.T) {
	outcome, err := domain.NewExitCheckOutcome(domain.ClaimPassed(), inScope, specifyPass)
	require.NoError(t, err)

	_, failed := domain.ClassifyFailure(outcome)

	assert.False(t, failed)
}

func TestChecksCode(t *testing.T) {
	want := map[domain.Stage]bool{
		domain.StageDiscovery: false, domain.StageSpecify: false, domain.StageImplement: false,
		domain.StageReview: true, domain.StageIntegrationTesting: true, domain.StageStressTesting: true, domain.StageFixer: false,
	}
	for _, stage := range domain.Stages() {
		assert.Equal(t, want[stage], stage.ChecksCode(), stage.String())
	}
}
