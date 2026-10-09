// Package guardagentactions is the PreToolUse hook: it denies agents the
// owner's commands, access to gofast's own files, and writes outside the
// current stage's write scope.
package guardagentactions

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/agentsessions"
	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// stageFacts is what the guard knows about the workflow.
type stageFacts struct {
	readable bool // false when the workflow state could not be read
	active   bool
	work     domain.WorkID
	stage    domain.Stage
	artifact domain.RepoPath
}

func unreadable() stageFacts   { return stageFacts{} }
func noActiveWork() stageFacts { return stageFacts{readable: true} }

func onStage(work domain.WorkID, stage domain.Stage, visit domain.Visit) stageFacts {
	return stageFacts{readable: true, active: true, work: work, stage: stage, artifact: domain.ArtifactFor(work, stage, visit)}
}

// Tool input fields, per https://code.claude.com/docs/en/hooks "PreToolUse
// input": Bash has command; Write and Edit have file_path; NotebookEdit has
// notebook_path.
type toolInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
}

var protectedNames = []string{"GF_ACTOR", "events.jsonl", "events.lock", "gf.log"}

var ownerOnlyReasons = map[string]string{
	"approve": "Only the owner can approve. Ask the owner to run `gf approve` in their own terminal.",
	"reject":  "Only the owner can reject a submission. Ask the owner to run `gf reject \"<feedback>\"` in their own terminal.",
	"extend":  "Only the owner can extend a budget. Ask the owner to run `gf extend <attempts>` in their own terminal.",
	"abandon": "Only the owner can abandon work. Ask the owner to run `gf abandon \"<reason>\"` in their own terminal.",
}

// decide is the guard's rule set:
//  1. Bash running an owner-only gf command: deny, everywhere.
//  2. Bash mentioning GF_ACTOR or gofast's log files: deny, everywhere.
//  3. Write, Edit or NotebookEdit outside the stage's write scope: deny, for
//     gofast stage agents and for the main session while it drives.
//
// Rules 1 and 2 need no workflow state, so they hold even when it cannot be
// read (fail closed); a Bash call whose input cannot be read is denied too.
// Rule 3 fails open when the state cannot be read, telling the owner.
func decide(in claudehooks.Input, facts stageFacts, driving bool, root string) claudehooks.Output {
	var tool toolInput
	inputErr := json.Unmarshal(in.ToolInput, &tool)

	switch in.ToolName {
	case "Bash":
		if inputErr != nil {
			return claudehooks.Deny("gofast could not read this command, so it was blocked. Try again with a plain command.")
		}
		if sub, ok := ownerOnlyCommand(tool.Command); ok {
			return claudehooks.Deny(ownerOnlyReasons[sub])
		}
		for _, name := range protectedNames {
			if strings.Contains(tool.Command, name) {
				return claudehooks.Deny(fmt.Sprintf("Agents may not touch %s: it belongs to gofast. Use `gf status` to read the workflow.", name))
			}
		}
		return claudehooks.Nothing()

	case "Write", "Edit", "NotebookEdit":
		if !scopeApplies(in, driving) {
			return claudehooks.Nothing()
		}
		if !facts.readable {
			return claudehooks.Tell("gofast could not read the workflow state, so this write was not checked against the stage's write scope. Details are in .gofast/gf.log.")
		}
		if !facts.active {
			return claudehooks.Deny("There is no active gofast work, so there is no stage to write for. Run /gofast:drive to start one.")
		}
		target := tool.FilePath
		if in.ToolName == "NotebookEdit" {
			target = tool.NotebookPath
		}
		if target != "" && !filepath.IsAbs(target) {
			target = filepath.Join(in.CWD, target)
		}
		p, err := domain.NewRepoPath(root, target)
		if err != nil {
			return claudehooks.Deny(fmt.Sprintf("During '%s' you may only write inside the repository; %s is outside it.", facts.stage, target))
		}
		if facts.stage.WriteScope().Allows(p, facts.work) {
			return claudehooks.Nothing()
		}
		return claudehooks.Deny(scopeReason(facts, p))
	}
	return claudehooks.Nothing()
}

// scopeApplies: rule 3 covers gofast stage agents, and the main session while
// it drives work.
func scopeApplies(in claudehooks.Input, driving bool) bool {
	if strings.HasPrefix(in.AgentType, "gofast:") {
		return true
	}
	return !in.InSubagent() && driving
}

func scopeReason(facts stageFacts, p domain.RepoPath) string {
	switch facts.stage.WriteScope() {
	case domain.WriteScopeArtifacts:
		return fmt.Sprintf("During '%s' you may only write the stage artifact `%s`.", facts.stage, facts.artifact)
	case domain.WriteScopeTestsAndArtifacts:
		return fmt.Sprintf("During '%s' you may only write tests and the stage artifact `%s`.", facts.stage, facts.artifact)
	default:
		return fmt.Sprintf("During '%s' you may write anything except gofast's own files in .gofast/ (%s); the stage artifact is `%s`.", facts.stage, p, facts.artifact)
	}
}

func NewCommand(dir func() string, getenv func(string) string) *cobra.Command {
	return &cobra.Command{
		Use:   "pre-tool-use",
		Short: "PreToolUse hook: guard agent actions",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			claudehooks.Run(claudehooks.Env{Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Dir: dir(), Getenv: getenv},
				func(repo claudehooks.Repo, in claudehooks.Input) claudehooks.Output {
					driving, err := agentsessions.Open(repo.Root).IsDriving(in.SessionID)
					if err != nil {
						repo.Logger.Error("guard could not read the driving marker", "error", err.Error())
					}
					out := decide(in, loadFacts(repo), driving, repo.Root)
					if out.HookSpecificOutput != nil {
						repo.Logger.Info("guard denied a tool call", "tool", in.ToolName, "agent", in.AgentType,
							"reason", out.HookSpecificOutput.PermissionDecisionReason)
					}
					return out
				})
			return nil
		},
	}
}

func loadFacts(repo claudehooks.Repo) stageFacts {
	var facts stageFacts
	err := repo.Store.Shared(func(s *eventlog.Snapshot) error {
		records, err := s.ReadAll()
		if err != nil {
			return err
		}
		work, _, err := eventlog.NewHistory(records).ActiveWork()
		if errors.Is(err, domain.ErrNoActiveWork) {
			facts = noActiveWork()
			return nil
		}
		if err != nil {
			return err
		}
		facts = onStage(work.ID(), work.CurrentStage(), work.CurrentVisit())
		return nil
	})
	if err != nil {
		repo.Logger.Error("guard could not read the workflow state", "error", err.Error())
		return unreadable()
	}
	return facts
}
