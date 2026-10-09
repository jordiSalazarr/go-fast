package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func TestParseWorkType(t *testing.T) {
	got, err := domain.ParseWorkType("fix-bug")
	require.NoError(t, err)
	assert.Equal(t, domain.WorkTypeFixBug, got)

	_, err = domain.ParseWorkType("feature")
	require.ErrorIs(t, err, domain.ErrUnknownWorkType)
	assert.Contains(t, err.Error(), "available: fix-bug")
}

func TestFixBugPath(t *testing.T) {
	path, err := domain.PathFor(domain.WorkTypeFixBug)
	require.NoError(t, err)

	type step struct {
		stage  domain.Stage
		gate   domain.Gate
		budget int
	}
	var got []step
	for _, s := range path.Steps() {
		got = append(got, step{s.Stage(), s.Gate(), s.Budget().Int()})
	}
	assert.Equal(t, []step{
		{domain.StageDiscovery, domain.GateHuman, 3},
		{domain.StageSpecify, domain.GateHuman, 3},
		{domain.StageImplement, domain.GateAuto, 3},
		{domain.StageReview, domain.GateAuto, 3},
		{domain.StageIntegrationTesting, domain.GateAuto, 3},
	}, got)
}

func TestValueConstructorsRejectInvalidInput(t *testing.T) {
	cases := map[string]struct {
		err    error
		target error
	}{
		"empty work id":      {second(domain.NewWorkID("")), domain.ErrInvalidWorkID},
		"work id with space": {second(domain.NewWorkID("a b")), domain.ErrInvalidWorkID},
		"unknown stage":      {second(domain.ParseStage("deploy")), domain.ErrUnknownStage},
		"unknown gate":       {second(domain.ParseGate("robot")), domain.ErrUnknownGate},
		"zero budget":        {second(domain.NewAttemptBudget(0)), domain.ErrInvalidAttemptBudget},
		"zero visit":         {second(domain.NewVisit(0)), domain.ErrInvalidVisit},
		"zero attempt":       {second(domain.NewAttempt(0)), domain.ErrInvalidAttempt},
		"negative count":     {second(domain.NewAttemptCount(-1)), domain.ErrInvalidAttempt},
		"blank description":  {second(domain.NewDescription("  ")), domain.ErrEmptyDescription},
		"blank feedback":     {second(domain.NewFeedback("")), domain.ErrEmptyFeedback},
		"blank reason":       {second(domain.NewReason("\n")), domain.ErrEmptyReason},
		"owner without name": {second(domain.NewOwner("", "a@b.c")), domain.ErrInvalidOwner},
		"empty branch":       {second(domain.NewBranch("")), domain.ErrInvalidBranch},
		"branch with space":  {second(domain.NewBranch("fix bug")), domain.ErrInvalidBranch},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, c.err, c.target)
		})
	}
}

func TestExitCheckOutcome_CombinesTheClaimWithTheScopeCheck(t *testing.T) {
	clean := domain.CheckWriteScope(domain.StageSpecify, workID, domain.NewChangedFiles(must(domain.NewRepoPath("/repo", "a_test.go"))))
	dirty := domain.CheckWriteScope(domain.StageSpecify, workID, domain.NewChangedFiles(must(domain.NewRepoPath("/repo", "a.go"))))
	cases := []struct {
		name   string
		claim  domain.AgentClaim
		scope  domain.ScopeCheck
		passed bool
		reason string
	}{
		{"passed, in scope", domain.ClaimPassed(), clean, true, ""},
		{"failed, in scope", domain.ClaimFailed(reasonOf("tests fail")), clean, false, "tests fail"},
		{"passed, out of scope", domain.ClaimPassed(), dirty, false, "Changed files outside the 'specify' write scope: a.go."},
		{"failed, out of scope", domain.ClaimFailed(reasonOf("tests fail")), dirty, false, "tests fail. Changed files outside the 'specify' write scope: a.go."},
		{"failed sentence, out of scope", domain.ClaimFailed(reasonOf("Tests fail!")), dirty, false, "Tests fail! Changed files outside the 'specify' write scope: a.go."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outcome := domain.NewExitCheckOutcome(c.claim, c.scope)
			assert.Equal(t, c.passed, outcome.Passed())
			assert.Equal(t, c.reason, outcome.Reason().String())
		})
	}
}

func second[T any](_ T, err error) error { return err }
