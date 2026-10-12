package observeskillloads

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
)

func skill(name string) claudehooks.Input {
	input, _ := json.Marshal(map[string]string{"skill": name})
	return claudehooks.Input{HookEventName: "PostToolUse", ToolName: "Skill", ToolInput: input}
}

func TestObservation(t *testing.T) {
	onOptimize := visit{active: true, work: WorkID, stage: domain.StageOptimize, visit: Visit1}

	got, ok := observation(skill("gofast:concurrency"), onOptimize)
	assert.True(t, ok)
	assert.Equal(t, domain.SkillLoaded{WorkID: WorkID, Stage: domain.StageOptimize, Visit: Visit1, Skill: "concurrency"}, got)

	got, ok = observation(skill("other:pdf"), onOptimize)
	assert.True(t, ok)
	assert.Equal(t, "other:pdf", got.Skill, "another plugin's skill keeps its full name")

	_, ok = observation(skill("gofast:concurrency"), visit{})
	assert.False(t, ok, "no active work: nothing to observe")
	_, ok = observation(claudehooks.Input{ToolName: "Read"}, onOptimize)
	assert.False(t, ok)
	_, ok = observation(skill(""), onOptimize)
	assert.False(t, ok)
}
