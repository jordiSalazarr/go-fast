package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gfinstructions "github.com/jordiSalazarr/go-fast/workflow/instructions"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// gofastPlugin points the hooks at this repository's plugin.
func gofastPlugin(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "plugin"))
	require.NoError(t, err)
	return map[string]string{"CLAUDE_PLUGIN_ROOT": root}
}

func TestInstructions_AStageVisitRecordsTheVersionsThatInstructedIt(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.expect("discovery", progress.AssignmentOpen, 0, 3)
	plugin := gofastPlugin(t)
	root := plugin["CLAUDE_PLUGIN_ROOT"]

	assert.Empty(t, r.hook(plugin, "agent-start", subagentEvent("SubagentStart", "gofast:discovery", false)))
	assert.Empty(t, r.hook(plugin, "post-tool-use", map[string]any{"hook_event_name": "PostToolUse", "agent_id": "subagent_01ABC123",
		"agent_type": "gofast:discovery", "tool_name": "Skill", "tool_input": map[string]any{"skill": "gofast:code-layout"},
		"tool_response": map[string]any{"success": true}}))

	agent, err := gfinstructions.AgentDefinition(root, "discovery")
	require.NoError(t, err)
	discovery, err := gfinstructions.Skill(root, "discovery")
	require.NoError(t, err)
	layout, err := gfinstructions.Skill(root, "code-layout")
	require.NoError(t, err)
	v := r.status()
	assert.Equal(t, []string{"discovery", "code-layout"}, v.Current.SkillsLoaded, "each skill once")
	assert.Equal(t, []progress.InstructionView{
		{Kind: "agent", Name: "gofast:discovery", Version: agent.Version.String()},
		{Kind: "skill", Name: "discovery", Load: "preloaded", Version: discovery.Version.String()},
		{Kind: "skill", Name: "code-layout", Load: "preloaded", Version: layout.Version.String()},
		{Kind: "skill", Name: "code-layout", Load: "invoked", Version: layout.Version.String()},
	}, v.Current.Instructions)
	assert.Contains(t, r.gf("status"), "Instruction versions this visit: agent gofast:discovery "+agent.Version.Short()+
		", discovery "+discovery.Version.Short()+", code-layout "+layout.Version.Short()+"\n")
	assert.Contains(t, r.eventTypes(), "AgentStarted")

	// Each version is kept, and keeping them is gf's write, not the visit's.
	for _, s := range []gfinstructions.Snapshot{agent.Snapshot, discovery, layout} {
		kept, ok, err := gfinstructions.Read(r.dir, s.Version)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, s, kept)
	}
	r.workOnStage()
	out := r.submitAsAgent("--passed")
	assert.NotContains(t, out, "write scope")
	r.expect("discovery", progress.AssignmentAwaitingApproval, 0, 3)
}

func TestInstructions_WithoutThePlugin_TheAgentIsRecordedUnversioned(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.expect("discovery", progress.AssignmentOpen, 0, 3)

	assert.Empty(t, r.hook(nil, "agent-start", subagentEvent("SubagentStart", "gofast:discovery", false)))

	assert.Equal(t, []progress.InstructionView{{Kind: "agent", Name: "gofast:discovery"}}, r.status().Current.Instructions)
	assert.Contains(t, r.gf("status"), "Instruction versions this visit: agent gofast:discovery unversioned\n")
	_, err := os.Stat(filepath.Join(r.dir, ".gofast", "instructions"))
	assert.True(t, os.IsNotExist(err))
}

func TestChecks_VerifiesTheKeptInstructionVersions(t *testing.T) {
	r := newRepo(t)
	assert.Contains(t, r.gf("checks"), "\nInstruction versions: none kept yet in .gofast/instructions.\n")

	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.expect("discovery", progress.AssignmentOpen, 0, 3)
	plugin := gofastPlugin(t)
	r.hook(plugin, "agent-start", subagentEvent("SubagentStart", "gofast:discovery", false))
	assert.Contains(t, r.gf("checks"), "\nInstruction versions: 3 kept in .gofast/instructions, each intact.\n", "the agent and its two preloads")

	skill, err := gfinstructions.Skill(plugin["CLAUDE_PLUGIN_ROOT"], "discovery")
	require.NoError(t, err)
	kept := ".gofast/instructions/" + skill.Version.String()
	r.write(kept+"/SKILL.md", "# Discovery, rewritten after the fact\n")
	out := r.gf("checks")
	assert.Contains(t, out, "Warning: "+kept+" changed after gf kept it")
	assert.Contains(t, out, "The event log's versions point at these copies: restore it with git.\n")
	assert.Contains(t, out, "\nInstruction versions: 3 kept in .gofast/instructions, 1 with problems (see the warnings above).\n")
}
