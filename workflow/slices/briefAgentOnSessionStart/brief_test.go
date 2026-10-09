package briefagentonsessionstart

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
)

func TestGivenReadableState_WhenASessionStarts_ThenClaudeGetsTheStatusAndHowToDrive(t *testing.T) {
	out := brief("fix-bug: login (w1)\n\nNext: Work on 'discovery'.\n", true)

	assert.Equal(t, claudehooks.Output{HookSpecificOutput: &claudehooks.HookSpecificOutput{
		HookEventName:     "SessionStart",
		AdditionalContext: "fix-bug: login (w1)\n\nNext: Work on 'discovery'.\n\nTo drive this work, run /gofast:drive.",
	}}, out)
}

func TestGivenUnreadableState_WhenASessionStarts_ThenClaudeAndTheOwnerAreTold(t *testing.T) {
	out := brief("", false)

	assert.Equal(t, "gofast could not read the workflow state (details are in .gofast/gf.log).\nTo drive this work, run /gofast:drive.",
		out.HookSpecificOutput.AdditionalContext)
	assert.Equal(t, "gofast could not read the workflow state. Details are in .gofast/gf.log.", out.SystemMessage)
}

func TestTheAgentShellIsMarkedAsAnAgents(t *testing.T) {
	assert.Equal(t, []string{"export GF_ACTOR=agent"}, agentShell)
}
