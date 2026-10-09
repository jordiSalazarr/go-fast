package keepagentonstage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/agentsessions"
	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
)

// Given / when / then on the pure decisions:
//
//	given facts about the current stage visit (and markers),
//	when the hook input arrives,
//	then this output.

// on builds the facts of a stage visit whose assignment has these events
// after AssignmentOpened. With opened unset, the assignment is not opened yet.
func on(t *testing.T, stage domain.Stage, opened bool, events ...domain.AssignmentEvent) assignmentFacts {
	t.Helper()
	facts := assignmentFacts{
		readable: true, active: true, stage: stage, assignment: AssignmentOn(stage),
		budget: Budget(3), artifact: domain.ArtifactFor(WorkID, stage, Visit1),
	}
	if !opened {
		return facts
	}
	history := append([]domain.AssignmentEvent{Opened(stage)}, events...)
	state, err := domain.RebuildAssignment(history)
	require.NoError(t, err)
	facts.state, facts.version = state, len(history)
	return facts
}

var (
	noActiveWork = assignmentFacts{readable: true}
	unreadable   = assignmentFacts{}
)

func stageAgentStops(stage domain.Stage, stopHookActive bool) claudehooks.Input {
	return claudehooks.Input{
		SessionID: "s1", HookEventName: "SubagentStop", AgentID: "agent-1",
		AgentType: "gofast:" + stage.String(), StopHookActive: stopHookActive,
	}
}

func mainStops(stopHookActive bool) claudehooks.Input {
	return claudehooks.Input{SessionID: "s1", HookEventName: "Stop", StopHookActive: stopHookActive}
}

// UserPromptExpansion

func TestDrivingStarts_OnTheDriveCommandOnly(t *testing.T) {
	assert.True(t, drivingStarts(claudehooks.Input{CommandName: "gofast:drive", CommandSource: "plugin"}))
	assert.False(t, drivingStarts(claudehooks.Input{CommandName: "drive", CommandSource: "project"}))
	assert.False(t, drivingStarts(claudehooks.Input{CommandName: "gofast:other"}))
	assert.False(t, drivingStarts(claudehooks.Input{CommandName: "review"}))
}

// SubagentStart

func TestAgentStart_RecordsTheAssignmentAndVersion(t *testing.T) {
	facts := on(t, domain.StageImplement, true, Failed(domain.StageImplement, 1, "a"))
	in := claudehooks.Input{AgentID: "agent-1", AgentType: "gofast:implement"}

	start, ok := agentStart(in, facts)

	require.True(t, ok)
	assert.Equal(t, agentsessions.AgentStart{Assignment: AssignmentOn(domain.StageImplement), Version: 2}, start)
}

func TestAgentStart_IgnoresNonGofastAgentsAndMissingWork(t *testing.T) {
	_, ok := agentStart(claudehooks.Input{AgentID: "a", AgentType: "Explore"}, on(t, domain.StageImplement, true))
	assert.False(t, ok)
	_, ok = agentStart(claudehooks.Input{AgentID: "a", AgentType: "gofast:implement"}, noActiveWork)
	assert.False(t, ok)
	_, ok = agentStart(claudehooks.Input{AgentID: "a", AgentType: "gofast:implement"}, unreadable)
	assert.False(t, ok)
}

// SubagentStop

func startedOn(stage domain.Stage, version int) agentsessions.AgentStart {
	return agentsessions.AgentStart{Assignment: AssignmentOn(stage), Version: version}
}

func TestSubagentStop_WithoutSubmitting_IsBlocked(t *testing.T) {
	facts := on(t, domain.StageImplement, true)

	out := subagentStop(stageAgentStops(domain.StageImplement, false), facts, startedOn(domain.StageImplement, 1), true)

	assert.Equal(t, claudehooks.Block("Submit before finishing: run `gf submit --passed` or `gf submit --failed \"<reason>\"`."), out)
}

func TestSubagentStop_WithoutSubmitting_ButStopHookActive_IsAllowedWithAMessage(t *testing.T) {
	facts := on(t, domain.StageImplement, true)

	out := subagentStop(stageAgentStops(domain.StageImplement, true), facts, startedOn(domain.StageImplement, 1), true)

	assert.Equal(t, claudehooks.Tell("gofast:implement finished without submitting 'implement'; run /gofast:drive to continue."), out)
}

func TestSubagentStop_AfterAFailedSubmission_IsAllowed(t *testing.T) {
	facts := on(t, domain.StageImplement, true, Failed(domain.StageImplement, 1, "tests fail"))

	out := subagentStop(stageAgentStops(domain.StageImplement, false), facts, startedOn(domain.StageImplement, 1), true)

	assert.Equal(t, claudehooks.Nothing(), out)
}

func TestSubagentStop_AfterSubmittingForApproval_IsAllowed(t *testing.T) {
	facts := on(t, domain.StageSpecify, true, SubmittedForApproval(domain.StageSpecify, 1))

	out := subagentStop(stageAgentStops(domain.StageSpecify, false), facts, startedOn(domain.StageSpecify, 1), true)

	assert.Equal(t, claudehooks.Nothing(), out)
}

func TestSubagentStop_AfterAcceptanceMovedTheWorkOn_IsAllowed(t *testing.T) {
	// The agent started on implement; its pass was accepted and review is open now.
	facts := on(t, domain.StageReview, true)

	out := subagentStop(stageAgentStops(domain.StageImplement, false), facts, startedOn(domain.StageImplement, 1), true)

	assert.Equal(t, claudehooks.Nothing(), out)
}

func TestSubagentStop_WithoutARecordedStart_OrForOtherAgents_IsAllowed(t *testing.T) {
	facts := on(t, domain.StageImplement, true)
	explore := claudehooks.Input{AgentID: "a", AgentType: "Explore", HookEventName: "SubagentStop"}

	assert.Equal(t, claudehooks.Nothing(), subagentStop(stageAgentStops(domain.StageImplement, false), facts, agentsessions.AgentStart{}, false))
	assert.Equal(t, claudehooks.Nothing(), subagentStop(explore, facts, startedOn(domain.StageImplement, 1), true))
}

func TestSubagentStop_WhenTheStateCannotBeRead_IsAllowedWithAMessage(t *testing.T) {
	out := subagentStop(stageAgentStops(domain.StageImplement, false), unreadable, startedOn(domain.StageImplement, 1), true)

	assert.Empty(t, out.Decision)
	assert.Contains(t, out.SystemMessage, "could not read the workflow state")
}

// Stop

func TestStop_NotDriving_NeverInterferes(t *testing.T) {
	out, clear := sessionStop(mainStops(false), on(t, domain.StageImplement, true), false)

	assert.Equal(t, claudehooks.Nothing(), out)
	assert.False(t, clear)
}

func TestStop_DrivingWithTheStageOpen_IsBlocked(t *testing.T) {
	facts := on(t, domain.StageImplement, true, Failed(domain.StageImplement, 1, "a"))

	out, clear := sessionStop(mainStops(false), facts, true)

	assert.Equal(t, claudehooks.Block("'implement' is still open (attempt 2 of 3). Run the gofast:implement agent."), out)
	assert.False(t, clear)
}

func TestStop_DrivingWithTheAssignmentNotOpenedYet_IsBlocked(t *testing.T) {
	out, _ := sessionStop(mainStops(false), on(t, domain.StageReview, false), true)

	assert.Equal(t, claudehooks.Block("'review' is still open (attempt 1 of 3). Run the gofast:review agent."), out)
}

func TestStop_DrivingWithTheStageOpen_ButStopHookActive_IsAllowedWithAMessage(t *testing.T) {
	out, clear := sessionStop(mainStops(true), on(t, domain.StageImplement, true), true)

	assert.Equal(t, claudehooks.Tell("Stopped while 'implement' is open; run /gofast:drive to continue."), out)
	assert.False(t, clear)
}

func TestStop_DrivingAndAwaitingApproval_IsAllowedTellingTheOwnerWhatToReview_AndStopsDriving(t *testing.T) {
	facts := on(t, domain.StageSpecify, true, SubmittedForApproval(domain.StageSpecify, 1))

	out, clear := sessionStop(mainStops(false), facts, true)

	assert.Equal(t, claudehooks.Tell("'specify' is waiting for your approval. Review `.gofast/works/w1/specify-v1.md`, then run `gf approve` or `gf reject \"<feedback>\"` in your own terminal. After you act, run /gofast:drive to continue."), out)
	assert.True(t, clear)
}

func TestStop_DrivingAndEscalated_IsAllowedTellingTheOwnerTheOptions_AndStopsDriving(t *testing.T) {
	facts := on(t, domain.StageImplement, true,
		Failed(domain.StageImplement, 1, "a"), Failed(domain.StageImplement, 2, "b"), Failed(domain.StageImplement, 3, "c"),
		Escalated(domain.StageImplement, 3, 3))

	out, clear := sessionStop(mainStops(false), facts, true)

	assert.Equal(t, claudehooks.Tell("Budget exhausted on 'implement'. Review `.gofast/works/w1/implement-v1.md`, then run `gf extend <n>` or `gf abandon \"<reason>\"` in your own terminal. After you act, run /gofast:drive to continue."), out)
	assert.True(t, clear)
}

func TestStop_DrivingWithNoActiveWork_IsAllowedAndStopsDriving(t *testing.T) {
	out, clear := sessionStop(mainStops(false), noActiveWork, true)

	assert.Equal(t, claudehooks.Nothing(), out)
	assert.True(t, clear)
}

func TestStop_DrivingWhenTheStateCannotBeRead_IsAllowedWithAMessage(t *testing.T) {
	out, clear := sessionStop(mainStops(false), unreadable, true)

	assert.Empty(t, out.Decision)
	assert.Contains(t, out.SystemMessage, "could not read the workflow state")
	assert.False(t, clear)
}
