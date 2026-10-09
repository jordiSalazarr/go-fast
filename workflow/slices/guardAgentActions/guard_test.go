package guardagentactions

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
)

const root = "/repo"

// Given / when / then on the pure decision:
//
//	given facts about the workflow and whether the session drives,
//	when the hook input arrives,
//	then this output.

func toolCall(agentType, tool string, input map[string]string) claudehooks.Input {
	raw, _ := json.Marshal(input)
	in := claudehooks.Input{SessionID: "s1", CWD: root, HookEventName: "PreToolUse", ToolName: tool, ToolInput: raw, AgentType: agentType}
	if agentType != "" {
		in.AgentID = "agent-1"
	}
	return in
}

func bash(command string) claudehooks.Input {
	return toolCall("gofast:implement", "Bash", map[string]string{"command": command})
}

func write(agentType, path string) claudehooks.Input {
	return toolCall(agentType, "Write", map[string]string{"file_path": path, "content": "x"})
}

func on(stage domain.Stage) stageFacts { return onStage(WorkID, stage, Visit1) }

func denied(reason string) claudehooks.Output { return claudehooks.Deny(reason) }

var allowed = claudehooks.Nothing()

func TestRule1_OwnerOnlyCommandsAreDeniedInAnySegment(t *testing.T) {
	approveReason := "Only the owner can approve. Ask the owner to run `gf approve` in their own terminal."
	for _, command := range []string{
		"gf approve",
		"/usr/local/bin/gf approve",
		"~/go/bin/gf --dir . approve",
		"gf --dir=/repo approve",
		"go test ./... && gf approve",
		"go vet ./...; gf approve",
		"echo y | gf approve",
		"true || gf approve",
		"echo $(gf approve)",
		"echo \"$(gf approve)\"",
		"echo `gf approve`",
		"(gf approve)",
		"env gf approve",
		"env -i HOME=/tmp gf approve",
		"FOO=1 gf approve",
		"sudo gf approve",
		"go run ./cmd/gf approve",
		"go run -race ./cmd/gf --dir . approve",
		"go run github.com/jordiSalazarr/go-fast/cmd/gf@latest approve",
		"bash -c 'gf approve'",
		"sh -lc \"cd /repo && gf approve\"",
		"eval gf approve",
		"if true; then gf approve; fi",
		"gf status\ngf approve",
	} {
		t.Run(command, func(t *testing.T) {
			assert.Equal(t, denied(approveReason), decide(bash(command), on(domain.StageSpecify), true, root))
		})
	}
}

func TestRule1_EachOwnerOnlyCommandHasItsOwnReason(t *testing.T) {
	cases := map[string]string{
		"gf reject 'no'":       "Only the owner can reject a submission. Ask the owner to run `gf reject \"<feedback>\"` in their own terminal.",
		"gf extend 2":          "Only the owner can extend a budget. Ask the owner to run `gf extend <attempts>` in their own terminal.",
		"gf abandon 'give up'": "Only the owner can abandon work. Ask the owner to run `gf abandon \"<reason>\"` in their own terminal.",
	}
	for command, reason := range cases {
		assert.Equal(t, denied(reason), decide(bash(command), on(domain.StageSpecify), true, root), command)
	}
}

func TestRule1_AgentCommandsAndMentionsAreAllowed(t *testing.T) {
	for _, command := range []string{
		"gf status --json",
		"gf submit --passed",
		"gf submit --failed 'approve flow broken'",
		"go test ./...",
		"git commit -m 'gf approve wording'",
		"grep -r approve .",
		"echo gfapprove",
	} {
		t.Run(command, func(t *testing.T) {
			assert.Equal(t, allowed, decide(bash(command), on(domain.StageImplement), true, root))
		})
	}
}

func TestRule2_GofastFilesAndGF_ACTORAreOffLimits(t *testing.T) {
	for _, command := range []string{
		"cat .gofast/events.jsonl",
		"echo '{}' >> .gofast/events.jsonl",
		"rm .gofast/events.lock",
		"tail .gofast/gf.log",
		"GF_ACTOR=owner gf status",
		"unset GF_ACTOR",
	} {
		t.Run(command, func(t *testing.T) {
			out := decide(bash(command), on(domain.StageImplement), true, root)
			assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision)
			assert.Contains(t, out.HookSpecificOutput.PermissionDecisionReason, "Agents may not touch")
		})
	}
}

func TestRules1And2_ApplyEverywhere_EvenOutsideADriveAndForOtherAgents(t *testing.T) {
	mainNotDriving := toolCall("", "Bash", map[string]string{"command": "gf approve"})
	explore := toolCall("Explore", "Bash", map[string]string{"command": "cat .gofast/events.jsonl"})

	assert.Equal(t, "deny", decide(mainNotDriving, noActiveWork(), false, root).HookSpecificOutput.PermissionDecision)
	assert.Equal(t, "deny", decide(explore, noActiveWork(), false, root).HookSpecificOutput.PermissionDecision)
}

func TestRules1And2_FailClosed_WhenTheStateOrInputCannotBeRead(t *testing.T) {
	assert.Equal(t, "deny", decide(bash("gf approve"), unreadable(), true, root).HookSpecificOutput.PermissionDecision)

	broken := claudehooks.Input{SessionID: "s1", ToolName: "Bash", ToolInput: json.RawMessage(`"not an object"`)}
	out := decide(broken, unreadable(), false, root)
	assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision)
}

func TestRule3_WriteScopeOfEachStage(t *testing.T) {
	cases := []struct {
		stage domain.Stage
		path  string
		want  claudehooks.Output
	}{
		{domain.StageDiscovery, "/repo/.gofast/works/w1/discovery-v1.md", allowed},
		{domain.StageDiscovery, "/repo/login/login.go",
			denied("During 'discovery' you may only write the stage artifact `.gofast/works/w1/discovery-v1.md`.")},
		{domain.StageDiscovery, "/repo/login/login_test.go",
			denied("During 'discovery' you may only write the stage artifact `.gofast/works/w1/discovery-v1.md`.")},
		{domain.StageSpecify, "/repo/login/login_test.go", allowed},
		{domain.StageSpecify, "/repo/login/testdata/case.json", allowed},
		{domain.StageSpecify, "/repo/.gofast/works/w1/specify-v1.md", allowed},
		{domain.StageSpecify, "/repo/login/login.go",
			denied("During 'specify' you may only write tests and the stage artifact `.gofast/works/w1/specify-v1.md`.")},
		{domain.StageImplement, "/repo/login/login.go", allowed},
		{domain.StageImplement, "/repo/.gofast/events.jsonl",
			denied("During 'implement' you may write anything except gofast's own files in .gofast/ (.gofast/events.jsonl); the stage artifact is `.gofast/works/w1/implement-v1.md`.")},
		{domain.StageImplement, "/repo/.gofast/works/w0/implement-v1.md",
			denied("During 'implement' you may write anything except gofast's own files in .gofast/ (.gofast/works/w0/implement-v1.md); the stage artifact is `.gofast/works/w1/implement-v1.md`.")},
		{domain.StageImplement, "/tmp/scratch.go",
			denied("During 'implement' you may only write inside the repository; /tmp/scratch.go is outside it.")},
		{domain.StageReview, "/repo/login/login.go",
			denied("During 'review' you may only write the stage artifact `.gofast/works/w1/review-v1.md`.")},
		{domain.StageIntegrationTesting, "/repo/.gofast/works/w1/integration-testing-v1.md", allowed},
	}
	for _, c := range cases {
		t.Run(c.stage.String()+" "+c.path, func(t *testing.T) {
			assert.Equal(t, c.want, decide(write("gofast:"+c.stage.String(), c.path), on(c.stage), false, root))
		})
	}
}

func TestRule3_RelativePathsResolveAgainstCwd(t *testing.T) {
	in := write("gofast:specify", "login/login.go")
	in.CWD = "/repo"

	out := decide(in, on(domain.StageSpecify), false, root)

	assert.Equal(t, "deny", out.HookSpecificOutput.PermissionDecision)
}

func TestRule3_EditAndNotebookEditAreGuardedToo(t *testing.T) {
	edit := toolCall("gofast:review", "Edit", map[string]string{"file_path": "/repo/a.go", "old_string": "a", "new_string": "b"})
	notebook := toolCall("gofast:review", "NotebookEdit", map[string]string{"notebook_path": "/repo/a.ipynb", "new_source": "x"})

	assert.Equal(t, "deny", decide(edit, on(domain.StageReview), false, root).HookSpecificOutput.PermissionDecision)
	assert.Equal(t, "deny", decide(notebook, on(domain.StageReview), false, root).HookSpecificOutput.PermissionDecision)
}

func TestRule3_AppliesToTheMainSessionOnlyWhileDriving(t *testing.T) {
	main := write("", "/repo/login/login.go")

	assert.Equal(t, "deny", decide(main, on(domain.StageReview), true, root).HookSpecificOutput.PermissionDecision)
	assert.Equal(t, allowed, decide(main, on(domain.StageReview), false, root))
}

func TestRule3_DoesNotApplyToNonGofastAgents(t *testing.T) {
	explore := write("Explore", "/repo/login/login.go")

	assert.Equal(t, allowed, decide(explore, on(domain.StageReview), true, root))
}

func TestRule3_FailsOpenWithAMessage_WhenTheStateCannotBeRead(t *testing.T) {
	out := decide(write("gofast:review", "/repo/login/login.go"), unreadable(), false, root)

	assert.Equal(t, claudehooks.Tell("gofast could not read the workflow state, so this write was not checked against the stage's write scope. Details are in .gofast/gf.log."), out)
}

func TestRule3_WithoutActiveWork_StageAgentsMayNotWrite(t *testing.T) {
	out := decide(write("gofast:implement", "/repo/a.go"), noActiveWork(), false, root)

	assert.Equal(t, denied("There is no active gofast work, so there is no stage to write for. Run /gofast:drive to start one."), out)
}

func TestOtherToolsAreNotGuarded(t *testing.T) {
	read := toolCall("gofast:review", "Read", map[string]string{"file_path": "/repo/.gofast/events.jsonl"})

	assert.Equal(t, allowed, decide(read, on(domain.StageReview), true, root))
}
