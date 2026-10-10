package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func TestEachStageRequiresItsExitChecks(t *testing.T) {
	want := map[domain.Stage][]domain.ExitCheck{
		domain.StageDiscovery: {domain.CheckArtifactWritten},
		domain.StageSpecify:   {domain.CheckArtifactWritten, domain.CheckSpecsFail},
		domain.StageImplement: {domain.CheckArtifactWritten, domain.CheckBuildPasses, domain.CheckSpecsPass,
			domain.CheckSpecsUntouched, domain.CheckSuitePasses},
		domain.StageReview:             {domain.CheckArtifactWritten, domain.CheckFormatted, domain.CheckBuildPasses, domain.CheckSuitePasses},
		domain.StageIntegrationTesting: {domain.CheckArtifactWritten, domain.CheckIntegrationPasses},
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
		"specs-untouched", "suite-passes", "formatted", "integration-passes"}, names)
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
		checkFailed(domain.CheckArtifactWritten, ".gofast/works/w1/implement-v1.md is missing"),
		checkFailed(domain.CheckFormatted, "not required on implement, so ignored"),
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
