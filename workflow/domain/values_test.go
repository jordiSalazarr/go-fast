package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func TestParseWorkType(t *testing.T) {
	for name, want := range map[string]domain.WorkType{
		"fix-bug": domain.WorkTypeFixBug, "new-feature": domain.WorkTypeNewFeature, "modify-behaviour": domain.WorkTypeModifyBehaviour,
	} {
		got, err := domain.ParseWorkType(name)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}

	_, err := domain.ParseWorkType("feature")
	require.ErrorIs(t, err, domain.ErrUnknownWorkType)
	assert.Contains(t, err.Error(), "available: fix-bug, modify-behaviour, new-feature")
}

type pathStep struct {
	stage    domain.Stage
	gate     domain.Gate
	budget   int
	optional bool
}

func stepsOf(t domain.WorkType) []pathStep {
	var got []pathStep
	for _, s := range must(domain.PathFor(t)).Steps() {
		got = append(got, pathStep{s.Stage(), s.Gate(), s.Budget().Int(), s.Optional()})
	}
	return got
}

// Given a work type, when its path is looked up, then it is this table.
func TestPaths(t *testing.T) {
	fixBug := []pathStep{
		{domain.StageDiscovery, domain.GateHuman, 3, false},
		{domain.StageSpecify, domain.GateHuman, 3, false},
		{domain.StageImplement, domain.GateAuto, 3, false},
		{domain.StageReview, domain.GateAuto, 3, false},
		{domain.StageIntegrationTesting, domain.GateAuto, 3, false},
		{domain.StageStressTesting, domain.GateAuto, 3, true},
	}
	designed := []pathStep{
		{domain.StageDiscovery, domain.GateHuman, 3, false},
		{domain.StageDesign, domain.GateHuman, 3, false},
		{domain.StageSpecify, domain.GateHuman, 3, false},
		{domain.StageImplement, domain.GateAuto, 3, false},
		{domain.StageReview, domain.GateAuto, 3, false},
		{domain.StageIntegrationTesting, domain.GateAuto, 3, false},
		{domain.StageStressTesting, domain.GateAuto, 3, true},
	}
	assert.Equal(t, fixBug, stepsOf(domain.WorkTypeFixBug))
	assert.Equal(t, designed, stepsOf(domain.WorkTypeNewFeature))
	assert.Equal(t, designed, stepsOf(domain.WorkTypeModifyBehaviour))

	assert.False(t, domain.WorkTypeFixBug.HasDesign())
	assert.True(t, domain.WorkTypeNewFeature.HasDesign())
	assert.True(t, domain.WorkTypeModifyBehaviour.HasDesign())
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

func second[T any](_ T, err error) error { return err }
