package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func input(kind domain.InputKind, path, content string) domain.WorkInput {
	return must(domain.NewWorkInput(kind, must(domain.ParseRepoPath(path)), domain.HashOf([]byte(content))))
}

var (
	specInput       = input(domain.InputSpec, "docs/wallets.md", "# Wallets")
	eventModelInput = input(domain.InputEventModel, "docs/wallets-model.json", `{"events": []}`)
)

// startFeature starts new-feature work from these inputs.
func startFeature(t *testing.T, inputs ...domain.WorkInput) []domain.WorkEvent {
	t.Helper()
	events, err := domain.StartWork(workID, domain.WorkTypeNewFeature, description, mainBranch, activeOn(t, mainBranch), inputs...)
	require.NoError(t, err)
	return events
}

// Given an event model, when feature work starts, then it records its
// inputs and enters event modelling first, then grill.
func TestStartWork_WithAnEventModel_EntersEventModellingFirst(t *testing.T) {
	history := startFeature(t, specInput, eventModelInput)

	started := history[0].(domain.WorkStarted)
	assert.Equal(t, []domain.WorkInput{specInput, eventModelInput}, started.Inputs)
	assert.Equal(t, domain.StageEntered{WorkID: workID, Stage: domain.StageEventModelling, Visit: visit1, Gate: domain.GateAuto, Budget: budget3}, history[1])
	w := asInProgress(t, givenWork(t, history...))
	assert.False(t, w.Skips(domain.StageEventModelling))
	assert.Equal(t, domain.AssignmentIDFor(workID, domain.StageEventModelling, visit1), w.FirstAssignment())
	model, ok := w.Input(domain.InputEventModel)
	require.True(t, ok)
	assert.Equal(t, eventModelInput, model)

	history = advance(t, history)
	assert.Equal(t, domain.StageGrill, history[len(history)-1].(domain.StageEntered).Stage)
}

// Given no event model, when feature work starts, then it skips event
// modelling and starts on grill.
func TestStartWork_WithoutAnEventModel_SkipsEventModelling(t *testing.T) {
	for name, inputs := range map[string][]domain.WorkInput{"a spec": {specInput}, "no input": nil} {
		history := startFeature(t, inputs...)

		assert.Equal(t, domain.StageGrill, history[1].(domain.StageEntered).Stage, name)
		w := asInProgress(t, givenWork(t, history...))
		assert.True(t, w.Skips(domain.StageEventModelling), name)
		_, ok := w.Input(domain.InputEventModel)
		assert.False(t, ok, name)
	}
}

func TestStartWork_RejectsInputsItCantStartFrom(t *testing.T) {
	_, err := domain.StartWork(workID, domain.WorkTypeFixBug, description, mainBranch, activeOn(t, mainBranch), eventModelInput)
	assert.ErrorIs(t, err, domain.ErrEventModelNotOnPath, "fix-bug has no event modelling")

	_, err = domain.StartWork(workID, domain.WorkTypeNewFeature, description, mainBranch, activeOn(t, mainBranch),
		specInput, input(domain.InputSpec, "docs/other.md", "x"))
	assert.ErrorIs(t, err, domain.ErrInvalidInput, "two specs")

	_, err = domain.StartWork(workID, domain.WorkTypeNewFeature, description, mainBranch, activeOn(t, mainBranch), domain.WorkInput{})
	assert.ErrorIs(t, err, domain.ErrMissingValue)
}

// The event model decides at start: a history that enters the other first
// stage can't be replayed.
func TestRebuildWork_TheFirstStageFollowsTheInputs(t *testing.T) {
	withModel := startFeature(t, eventModelInput)
	withoutModel := startFeature(t)

	_, err := domain.RebuildWork(append([]domain.WorkEvent{withModel[0]}, withoutModel[1:]...))
	assert.ErrorIs(t, err, domain.ErrInconsistentHistory)
	_, err = domain.RebuildWork(append([]domain.WorkEvent{withoutModel[0]}, withModel[1:]...))
	assert.ErrorIs(t, err, domain.ErrInconsistentHistory)
}

func TestWorkInput_KnowsWhetherItsContentChanged(t *testing.T) {
	assert.False(t, specInput.ChangedSince([]byte("# Wallets")))
	assert.True(t, specInput.ChangedSince([]byte("# Wallets, v2")))
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", domain.HashOf(nil).String())
	_, err := domain.ParseContentHash("E3B0")
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestParseRepoPath_AcceptsOnlyCleanRelativePaths(t *testing.T) {
	assert.Equal(t, "docs/wallets.md", must(domain.ParseRepoPath("docs/wallets.md")).String())
	for _, bad := range []string{"", ".", "/etc/passwd", "../x", "..", "docs/../x", "docs//x", "./docs"} {
		_, err := domain.ParseRepoPath(bad)
		assert.ErrorIs(t, err, domain.ErrInvalidRepoPath, bad)
	}
}
