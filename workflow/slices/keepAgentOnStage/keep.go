// Package keepagentonstage keeps a driving session working until the owner is
// needed, and keeps a stage agent from finishing without submitting. Outside
// a drive it never interferes.
//
// Hooks: UserPromptExpansion (drive starts), SubagentStart (a stage agent
// starts), SubagentStop (a stage agent finishes), Stop (the main session
// finishes).
package keepagentonstage

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/agentsessions"
	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// The drive skill's command, as UserPromptExpansion reports it in
// command_name (https://code.claude.com/docs/en/hooks "UserPromptExpansion
// input"). Observed with Claude Code 2.1.286: command_name "gofast:drive",
// command_source "plugin".
const (
	driveCommand = "gofast:drive"
	agentPrefix  = "gofast:"
)

// assignmentFacts is what the hooks know about the current stage visit.
type assignmentFacts struct {
	readable   bool // false when the workflow state could not be read
	active     bool
	stage      domain.Stage
	assignment domain.AssignmentID
	version    int                    // the assignment's stream version; 0 before it is opened
	state      domain.AssignmentState // nil before it is opened
	budget     domain.AttemptBudget   // the path's budget, for an assignment not opened yet
	artifact   domain.RepoPath
}

// drivingStarts: the owner ran /gofast:drive.
func drivingStarts(in claudehooks.Input) bool {
	return in.CommandName == driveCommand
}

// agentStart is what a starting stage agent is recorded as working on.
func agentStart(in claudehooks.Input, facts assignmentFacts) (agentsessions.AgentStart, bool) {
	if !strings.HasPrefix(in.AgentType, agentPrefix) || !facts.readable || !facts.active {
		return agentsessions.AgentStart{}, false
	}
	return agentsessions.AgentStart{Assignment: facts.assignment, Version: facts.version}, true
}

// subagentStop blocks a stage agent that is finishing without having
// submitted: its assignment is still open and unchanged since it started.
func subagentStop(in claudehooks.Input, facts assignmentFacts, start agentsessions.AgentStart, recorded bool) claudehooks.Output {
	if !strings.HasPrefix(in.AgentType, agentPrefix) || !recorded {
		return claudehooks.Nothing()
	}
	if !facts.readable {
		return claudehooks.Tell("gofast could not read the workflow state, so it let " + in.AgentType + " finish. Details are in .gofast/gf.log.")
	}
	untouched := facts.active && facts.assignment == start.Assignment && facts.version == start.Version
	if !untouched || !isOpen(facts.state) {
		return claudehooks.Nothing()
	}
	if in.StopHookActive {
		return claudehooks.Tell(fmt.Sprintf("%s finished without submitting '%s'; run /gofast:drive to continue.", in.AgentType, facts.stage))
	}
	return claudehooks.Block("Submit before finishing: run `gf submit --passed` or `gf submit --failed \"<reason>\"`.")
}

// sessionStop keeps a driving session going while the stage is open, and lets
// it stop, saying what the owner must do, once the owner is needed. Drive mode
// ends when the owner is needed or the work is over: it reports whether the
// session's driving marker should be cleared.
func sessionStop(in claudehooks.Input, facts assignmentFacts, driving bool) (out claudehooks.Output, clearDriving bool) {
	if !driving || in.InSubagent() {
		return claudehooks.Nothing(), false
	}
	if !facts.readable {
		return claudehooks.Tell("gofast could not read the workflow state, so it let the session stop. Details are in .gofast/gf.log."), false
	}
	if !facts.active {
		return claudehooks.Nothing(), true
	}
	switch s := facts.state.(type) {
	case nil, domain.Open:
		if in.StopHookActive {
			return claudehooks.Tell(fmt.Sprintf("Stopped while '%s' is open; run /gofast:drive to continue.", facts.stage)), false
		}
		attempt, budget := 1, facts.budget.Int()
		if open, ok := s.(domain.Open); ok {
			attempt, budget = open.AttemptsUsed().Int()+1, open.Budget().Int()
		}
		return claudehooks.Block(fmt.Sprintf("'%s' is still open (attempt %d of %d). Run the %s%s agent.",
			facts.stage, attempt, budget, agentPrefix, facts.stage)), false
	case domain.AwaitingApproval:
		return claudehooks.Tell(fmt.Sprintf("'%s' is waiting for your approval. Review `%s`, then run `gf approve` or `gf reject \"<feedback>\"` in your own terminal. %s",
			facts.stage, facts.artifact, resumeHint)), true
	case domain.Escalated:
		return claudehooks.Tell(fmt.Sprintf("Budget exhausted on '%s'. Review `%s`, then run `gf extend <n>` or `gf abandon \"<reason>\"` in your own terminal. %s",
			facts.stage, facts.artifact, resumeHint)), true
	}
	return claudehooks.Nothing(), false
}

// resumeHint ends the message when drive mode ends because the owner is needed.
const resumeHint = "After you act, run /gofast:drive to continue."

func isOpen(state domain.AssignmentState) bool {
	switch state.(type) {
	case nil, domain.Open:
		return true
	}
	return false
}

// NewCommands returns the `gf hook` subcommands of this slice.
func NewCommands(dir func() string, getenv func(string) string, branchOf func(root string) (domain.Branch, error)) []*cobra.Command {
	hook := func(use, short string, handle func(claudehooks.Repo, claudehooks.Input) claudehooks.Output) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				claudehooks.Run(claudehooks.Env{Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Dir: dir(), Getenv: getenv}, handle)
				return nil
			},
		}
	}
	return []*cobra.Command{
		hook("user-prompt-expansion", "UserPromptExpansion hook: record a session as driving",
			func(repo claudehooks.Repo, in claudehooks.Input) claudehooks.Output {
				if drivingStarts(in) {
					logIfFailed(repo, "mark driving", agentsessions.Open(repo.Root).MarkDriving(in.SessionID))
				}
				return claudehooks.Nothing()
			}),
		hook("subagent-start", "SubagentStart hook: record what a stage agent starts on",
			func(repo claudehooks.Repo, in claudehooks.Input) claudehooks.Output {
				if start, ok := agentStart(in, loadFacts(repo, branchOf)); ok {
					logIfFailed(repo, "record agent start", agentsessions.Open(repo.Root).RecordAgentStart(in.AgentID, start))
				}
				return claudehooks.Nothing()
			}),
		hook("subagent-stop", "SubagentStop hook: a stage agent must submit before finishing",
			func(repo claudehooks.Repo, in claudehooks.Input) claudehooks.Output {
				if !strings.HasPrefix(in.AgentType, agentPrefix) {
					return claudehooks.Nothing()
				}
				markers := agentsessions.Open(repo.Root)
				start, recorded, err := markers.AgentStart(in.AgentID)
				logIfFailed(repo, "read agent start", err)
				out := subagentStop(in, loadFacts(repo, branchOf), start, recorded)
				if out.Decision != "block" {
					logIfFailed(repo, "forget agent", markers.ForgetAgent(in.AgentID))
				}
				return out
			}),
		hook("stop", "Stop hook: keep a driving session going until the owner is needed",
			func(repo claudehooks.Repo, in claudehooks.Input) claudehooks.Output {
				markers := agentsessions.Open(repo.Root)
				driving, err := markers.IsDriving(in.SessionID)
				logIfFailed(repo, "read driving marker", err)
				out, clear := sessionStop(in, loadFacts(repo, branchOf), driving)
				if clear {
					logIfFailed(repo, "clear driving marker", markers.ClearDriving(in.SessionID))
				}
				return out
			}),
	}
}

func logIfFailed(repo claudehooks.Repo, what string, err error) {
	if err != nil {
		repo.Logger.Error("could not "+what, "error", err.Error())
	}
}

func loadFacts(repo claudehooks.Repo, branchOf func(root string) (domain.Branch, error)) assignmentFacts {
	branch, err := branchOf(repo.Root)
	if err != nil {
		repo.Logger.Error("could not tell the current branch", "error", err.Error())
		return assignmentFacts{}
	}
	var facts assignmentFacts
	err = repo.Store.Shared(func(s *eventlog.Snapshot) error {
		records, err := s.ReadAll()
		if err != nil {
			return err
		}
		history := eventlog.NewHistory(records)
		fact, err := history.ActiveWorkOn(branch)
		if err != nil {
			return err
		}
		work, err := fact.Work()
		if errors.Is(err, domain.ErrNoActiveWork) {
			facts = assignmentFacts{readable: true}
			return nil
		}
		if err != nil {
			return err
		}
		facts = assignmentFacts{
			readable: true, active: true, stage: work.CurrentStage(), assignment: work.CurrentAssignment(),
			artifact: domain.ArtifactFor(work.ID(), work.CurrentStage(), work.CurrentVisit()),
		}
		for _, step := range work.Path().Steps() {
			if step.Stage() == work.CurrentStage() {
				facts.budget = step.Budget()
			}
		}
		state, version, err := history.Assignment(facts.assignment)
		switch {
		case errors.Is(err, eventlog.ErrStreamNotFound):
			// Not opened yet: the next gf command's automations open it.
		case err != nil:
			return err
		default:
			facts.state, facts.version = state, version
		}
		return nil
	})
	if err != nil {
		repo.Logger.Error("could not read the workflow state", "error", err.Error())
		return assignmentFacts{}
	}
	return facts
}
