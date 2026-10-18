package observeskillloads

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/instructions"
)

func skill(name string) claudehooks.Input {
	input, _ := json.Marshal(map[string]string{"skill": name})
	return claudehooks.Input{HookEventName: "PostToolUse", ToolName: "Skill", ToolInput: input}
}

func TestObservation(t *testing.T) {
	onOptimize := visit{active: true, work: WorkID, stage: domain.StageOptimize, visit: Visit1}

	got, ok := observation(skill("gofast:concurrency"), onOptimize)
	assert.True(t, ok)
	assert.Equal(t, domain.SkillLoaded{WorkID: WorkID, Stage: domain.StageOptimize, Visit: Visit1, Skill: "concurrency", Load: domain.SkillInvoked}, got)

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

// gofastPlugin has the specify agent, preloading one of its skills and
// another plugin's.
func gofastPlugin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write(t, root, "agents/specify.md", "---\nname: specify\nskills:\n  - gofast:specify\n  - other:pdf\n---\n\nYou specify.\n")
	write(t, root, "skills/specify/SKILL.md", "# Specify\n")
	write(t, root, ".claude-plugin/plugin.json", `{"version": "0.2.0"}`)
	return root
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
}

func repo(t *testing.T) claudehooks.Repo {
	return claudehooks.Repo{Root: t.TempDir(), Logger: slog.New(slog.DiscardHandler)}
}

func TestAgentStart_RecordsTheAgentAndItsPreloads_WithTheirVersions_AndKeepsThem(t *testing.T) {
	root, r := gofastPlugin(t), repo(t)
	onSpecify := visit{active: true, work: WorkID, stage: domain.StageSpecify, visit: Visit1}

	events := plugin{root: root, repo: r}.agentStart(claudehooks.Input{AgentID: "a1", AgentType: "gofast:specify"}, onSpecify)

	definition := must(instructions.AgentDefinition(root, "specify"))
	skill := must(instructions.Skill(root, "specify"))
	require.Len(t, events, 3)
	started := events[0].(domain.AgentStarted)
	assert.Equal(t, domain.AgentStarted{WorkID: WorkID, Stage: domain.StageSpecify, Visit: Visit1, Agent: "gofast:specify",
		Version: definition.Version, PluginVersion: "0.2.0", GfVersion: instructions.GfVersion()}, started)
	assert.Equal(t, domain.SkillLoaded{WorkID: WorkID, Stage: domain.StageSpecify, Visit: Visit1, Skill: "specify",
		Load: domain.SkillPreloaded, Version: skill.Version}, events[1])
	assert.Equal(t, domain.SkillLoaded{WorkID: WorkID, Stage: domain.StageSpecify, Visit: Visit1, Skill: "other:pdf",
		Load: domain.SkillPreloaded}, events[2], "another plugin's skill: no version")
	for _, v := range []domain.InstructionsVersion{definition.Version, skill.Version} {
		_, kept, err := instructions.Read(r.Root, v)
		require.NoError(t, err)
		assert.True(t, kept, "every recorded version is kept")
	}
}

func TestAgentStart_WithoutThePlugin_RecordsTheAgentOnly(t *testing.T) {
	onSpecify := visit{active: true, work: WorkID, stage: domain.StageSpecify, visit: Visit1}
	in := claudehooks.Input{AgentID: "a1", AgentType: "gofast:specify"}

	events := plugin{repo: repo(t)}.agentStart(in, onSpecify)
	require.Len(t, events, 1)
	assert.True(t, events[0].(domain.AgentStarted).Version.IsZero())

	events = plugin{root: t.TempDir(), repo: repo(t)}.agentStart(in, onSpecify)
	require.Len(t, events, 1, "an agent gf can't read: no version, no preloads")

	assert.Empty(t, plugin{repo: repo(t)}.agentStart(claudehooks.Input{AgentType: "other:helper"}, onSpecify), "not a gofast agent")
	assert.Empty(t, plugin{repo: repo(t)}.agentStart(in, visit{}), "no active work")
}

func TestSkillVersion(t *testing.T) {
	root := gofastPlugin(t)
	p := plugin{root: root, repo: repo(t)}
	assert.Equal(t, must(instructions.Skill(root, "specify")).Version, p.skillVersion("specify"))
	assert.True(t, p.skillVersion("other:pdf").IsZero())
	assert.True(t, p.skillVersion("postgres").IsZero(), "not in the plugin")
	assert.True(t, plugin{repo: repo(t)}.skillVersion("specify").IsZero(), "no plugin root")
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
