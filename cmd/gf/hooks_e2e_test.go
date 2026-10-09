package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hook commands end to end: `gf hook <event>` runs in-process against a temp
// git repository, reading stdin JSON shaped like the examples in
// https://code.claude.com/docs/en/hooks, and stdout is asserted exactly.
// No --dir: the repository is found from the input's cwd, as in Claude Code.

func (r *repo) hook(env map[string]string, event string, input map[string]any) string {
	r.t.Helper()
	input["cwd"] = r.dir
	if _, ok := input["session_id"]; !ok {
		input["session_id"] = "abc123"
	}
	input["transcript_path"] = "/Users/.../.claude/projects/.../transcript.jsonl"
	input["permission_mode"] = "default"
	raw, err := json.Marshal(input)
	require.NoError(r.t, err)
	var stdout, stderr bytes.Buffer
	code := execute([]string{"hook", event}, func(k string) string { return env[k] }, bytes.NewReader(raw), &stdout, &stderr)
	require.Equal(r.t, 0, code, stderr.String())
	assert.Empty(r.t, stderr.String())
	return stdout.String()
}

func preToolUse(agentType, tool string, toolInput map[string]any) map[string]any {
	in := map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": toolInput, "tool_use_id": "toolu_01ABC123"}
	if agentType != "" {
		in["agent_id"], in["agent_type"] = "subagent_01ABC123", agentType
	}
	return in
}

func subagentEvent(event, agentType string, stopHookActive bool) map[string]any {
	in := map[string]any{"hook_event_name": event, "agent_id": "subagent_01ABC123", "agent_type": agentType}
	if event == "SubagentStop" {
		in["stop_hook_active"] = stopHookActive
		in["last_assistant_message"] = "Done."
	}
	return in
}

func stop(stopHookActive bool) map[string]any {
	return map[string]any{"hook_event_name": "Stop", "stop_hook_active": stopHookActive, "last_assistant_message": "Done."}
}

func driveExpansion() map[string]any {
	return map[string]any{
		"hook_event_name": "UserPromptExpansion", "expansion_type": "slash_command",
		"command_name": "gofast:drive", "command_args": "", "command_source": "plugin", "prompt": "/gofast:drive",
	}
}

// Expected outputs are spelled out in the order gf writes their fields.

func deny(reason string) string {
	raw, _ := json.Marshal(reason)
	return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":` + string(raw) + "}}\n"
}

func block(reason string) string {
	raw, _ := json.Marshal(reason)
	return `{"decision":"block","reason":` + string(raw) + "}\n"
}

func tell(message string) string {
	raw, _ := json.Marshal(message)
	return `{"systemMessage":` + string(raw) + "}\n"
}

func TestHooks_AreSilentInARepositoryWithoutGofast(t *testing.T) {
	r := newRepo(t)
	for event, input := range map[string]map[string]any{
		"pre-tool-use":          preToolUse("", "Bash", map[string]any{"command": "gf approve"}),
		"session-start":         {"hook_event_name": "SessionStart", "source": "startup"},
		"user-prompt-expansion": driveExpansion(),
		"subagent-start":        subagentEvent("SubagentStart", "gofast:implement", false),
		"subagent-stop":         subagentEvent("SubagentStop", "gofast:implement", false),
		"stop":                  stop(false),
	} {
		assert.Empty(t, r.hook(nil, event, input), event)
	}
	_, err := os.Stat(filepath.Join(r.dir, ".gofast"))
	assert.True(t, os.IsNotExist(err), "hooks must not create .gofast/")
}

func TestHookPreToolUse_EndToEnd(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	artifact := r.status().Current.Artifact

	assert.Equal(t,
		deny("Only the owner can approve. Ask the owner to run `gf approve` in their own terminal."),
		r.hook(nil, "pre-tool-use", preToolUse("gofast:discovery", "Bash", map[string]any{"command": "go test ./... && env gf approve", "description": "test"})))
	assert.Equal(t,
		deny("Agents may not touch events.jsonl: it belongs to gofast. Use `gf status` to read the workflow."),
		r.hook(nil, "pre-tool-use", preToolUse("", "Bash", map[string]any{"command": "cat .gofast/events.jsonl"})))
	assert.Empty(t, r.hook(nil, "pre-tool-use", preToolUse("gofast:discovery", "Bash", map[string]any{"command": "gf status --json"})))

	assert.Equal(t,
		deny("During 'discovery' you may only write the stage artifact `"+artifact+"`."),
		r.hook(nil, "pre-tool-use", preToolUse("gofast:discovery", "Write", map[string]any{"file_path": filepath.Join(r.dir, "login.go"), "content": "x"})))
	assert.Empty(t, r.hook(nil, "pre-tool-use", preToolUse("gofast:discovery", "Write", map[string]any{"file_path": filepath.Join(r.dir, artifact), "content": "x"})))

	// The main session is only scoped while it drives.
	mainWrite := preToolUse("", "Write", map[string]any{"file_path": filepath.Join(r.dir, "login.go"), "content": "x"})
	assert.Empty(t, r.hook(nil, "pre-tool-use", mainWrite))
	assert.Empty(t, r.hook(nil, "user-prompt-expansion", driveExpansion()))
	assert.Equal(t, deny("During 'discovery' you may only write the stage artifact `"+artifact+"`."), r.hook(nil, "pre-tool-use", mainWrite))
}

func TestHookPreToolUse_WithUnreadableState_FailsClosedForCommandsAndOpenForWrites(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	require.NoError(t, os.WriteFile(filepath.Join(r.dir, ".gofast", "events.jsonl"), []byte("not json\n"), 0o644))

	assert.Equal(t,
		deny("Only the owner can approve. Ask the owner to run `gf approve` in their own terminal."),
		r.hook(nil, "pre-tool-use", preToolUse("gofast:discovery", "Bash", map[string]any{"command": "gf approve"})))
	assert.Equal(t,
		tell("gofast could not read the workflow state, so this write was not checked against the stage's write scope. Details are in .gofast/gf.log."),
		r.hook(nil, "pre-tool-use", preToolUse("gofast:discovery", "Write", map[string]any{"file_path": filepath.Join(r.dir, "a.go"), "content": "x"})))
	log, err := os.ReadFile(filepath.Join(r.dir, ".gofast", "gf.log"))
	require.NoError(t, err)
	assert.Contains(t, string(log), "malformed event log at line 1")
}

func TestHookSessionStart_EndToEnd(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	envFile := filepath.Join(t.TempDir(), "claude-env")
	statusText := r.gf("status")

	out := r.hook(map[string]string{"CLAUDE_ENV_FILE": envFile}, "session-start",
		map[string]any{"hook_event_name": "SessionStart", "source": "startup", "model": "claude-opus-5-5"})

	type specific struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	want, _ := json.Marshal(struct {
		HookSpecificOutput specific `json:"hookSpecificOutput"`
	}{specific{"SessionStart", strings.TrimRight(statusText, "\n") + "\n\nTo drive this work, run /gofast:drive."}})
	assert.Equal(t, string(want)+"\n", out)
	env, err := os.ReadFile(envFile)
	require.NoError(t, err)
	assert.Equal(t, "export GF_ACTOR=agent\n", string(env))
}

func TestHookStageAgentLifecycle_EndToEnd(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.gf("submit", "--passed")
	r.gf("approve")
	r.gf("submit", "--passed")
	r.gf("approve") // now on implement, an auto gate
	artifact := r.status().Current.Artifact

	// A non-driving session is never held.
	assert.Empty(t, r.hook(nil, "stop", stop(false)))

	// The owner runs /gofast:drive: the session now keeps going while the stage is open.
	assert.Empty(t, r.hook(nil, "user-prompt-expansion", driveExpansion()))
	assert.Equal(t, block("'implement' is still open (attempt 1 of 3). Run the gofast:implement agent."), r.hook(nil, "stop", stop(false)))
	assert.Equal(t, tell("Stopped while 'implement' is open; run /gofast:drive to continue."), r.hook(nil, "stop", stop(true)))

	// The stage agent can't finish without submitting.
	assert.Empty(t, r.hook(nil, "subagent-start", subagentEvent("SubagentStart", "gofast:implement", false)))
	assert.Equal(t,
		block("Submit before finishing: run `gf submit --passed` or `gf submit --failed \"<reason>\"`."),
		r.hook(nil, "subagent-stop", subagentEvent("SubagentStop", "gofast:implement", false)))
	assert.Equal(t,
		tell("gofast:implement finished without submitting 'implement'; run /gofast:drive to continue."),
		r.hook(nil, "subagent-stop", subagentEvent("SubagentStop", "gofast:implement", true)))

	// Once it submits, it may finish; the attempt is used.
	assert.Empty(t, r.hook(nil, "subagent-start", subagentEvent("SubagentStart", "gofast:implement", false)))
	r.run(asAgent, "submit", "--failed", "unit tests fail")
	assert.Empty(t, r.hook(nil, "subagent-stop", subagentEvent("SubagentStop", "gofast:implement", false)))
	assert.Equal(t, block("'implement' is still open (attempt 2 of 3). Run the gofast:implement agent."), r.hook(nil, "stop", stop(false)))

	// Escalation lets the session stop and tells the owner the options.
	r.run(asAgent, "submit", "--failed", "still failing")
	r.run(asAgent, "submit", "--failed", "still failing")
	assert.Equal(t,
		tell("Budget exhausted on 'implement'. Review `"+artifact+"`, then run `gf extend <n>` or `gf abandon \"<reason>\"` in your own terminal."),
		r.hook(nil, "stop", stop(false)))

	// Abandoned: no active work, the session stops and is no longer driving.
	r.gf("abandon", "wrong approach")
	assert.Empty(t, r.hook(nil, "stop", stop(false)))
	r.gf("start", "--type", "fix-bug", "next bug")
	assert.Empty(t, r.hook(nil, "stop", stop(false)), "driving was cleared")

	// Non-gofast subagents are never held.
	assert.Empty(t, r.hook(nil, "subagent-stop", subagentEvent("SubagentStop", "Explore", false)))
}

func TestHookStop_WaitingForApproval_TellsTheOwnerWhatToReview(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	r.gf("submit", "--passed")
	artifact := r.status().Current.Artifact
	r.hook(nil, "user-prompt-expansion", driveExpansion())

	assert.Equal(t,
		tell("'discovery' is waiting for your approval. Review `"+artifact+"`, then run `gf approve` or `gf reject \"<feedback>\"` in your own terminal."),
		r.hook(nil, "stop", stop(false)))
}

func TestHookMarkers_StayOutOfTheEventLog(t *testing.T) {
	r := newRepo(t)
	r.gf("start", "--type", "fix-bug", "login button does nothing")
	before := r.eventTypes()

	r.hook(nil, "user-prompt-expansion", driveExpansion())
	r.hook(nil, "subagent-start", subagentEvent("SubagentStart", "gofast:discovery", false))

	assert.Equal(t, before, r.eventTypes())
	entries, err := os.ReadDir(filepath.Join(r.dir, ".gofast", "runtime"))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "driving and agents markers")
	ignore, _ := os.ReadFile(filepath.Join(r.dir, ".gofast", ".gitignore"))
	assert.Contains(t, string(ignore), "runtime/")
}
