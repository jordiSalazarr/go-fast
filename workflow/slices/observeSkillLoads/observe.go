// Package observeskillloads is the PostToolUse hook on the Skill tool: it
// records each skill a stage visit loaded on the work's observations
// stream, for gf status. It never blocks anything.
package observeskillloads

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// The Skill tool's input, as PostToolUse receives it in tool_input
// (https://code.claude.com/docs/en/hooks "PostToolUse input": tool_name,
// tool_input, tool_response). Its field is pinned to the Skill tool's input
// schema (Claude Code 2.1.286): skill, the skill's name, a plugin's skills
// as "<plugin>:<name>".
type skillInput struct {
	Skill string `json:"skill"`
}

// visit is the stage visit the branch's active work is on, if any.
type visit struct {
	active bool
	work   domain.WorkID
	stage  domain.Stage
	visit  domain.Visit
}

// observation decides what a PostToolUse call observed: a skill loaded
// during the active work's current stage visit. Gofast's own skills are
// named as their catalog names them.
func observation(in claudehooks.Input, v visit) (domain.SkillLoaded, bool) {
	if in.ToolName != "Skill" || !v.active {
		return domain.SkillLoaded{}, false
	}
	var tool skillInput
	if err := json.Unmarshal(in.ToolInput, &tool); err != nil || tool.Skill == "" {
		return domain.SkillLoaded{}, false
	}
	name := tool.Skill
	if catalog, ok := domain.CatalogSkill(tool.Skill); ok {
		name = catalog
	}
	return domain.SkillLoaded{WorkID: v.work, Stage: v.stage, Visit: v.visit, Skill: name}, true
}

func NewCommand(dir func() string, getenv func(string) string, branchOf func(root string) (domain.Branch, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "post-tool-use",
		Short: "PostToolUse hook: record the skills a stage visit loads",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			claudehooks.Run(claudehooks.Env{Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Dir: dir(), Getenv: getenv},
				func(repo claudehooks.Repo, in claudehooks.Input) claudehooks.Output {
					if err := record(repo, in, branchOf); err != nil {
						repo.Logger.Error("could not record a skill load", "error", err.Error())
					}
					return claudehooks.Nothing()
				})
			return nil
		},
	}
}

func record(repo claudehooks.Repo, in claudehooks.Input, branchOf func(root string) (domain.Branch, error)) error {
	if in.ToolName != "Skill" {
		return nil
	}
	branch, err := branchOf(repo.Root)
	if err != nil {
		return err
	}
	return repo.Store.Exclusive(func(s *eventlog.Session) error {
		records, err := s.ReadAll()
		if err != nil {
			return err
		}
		history := eventlog.NewHistory(records)
		fact, err := history.ActiveWorkOn(branch)
		if err != nil {
			return err
		}
		var v visit
		work, err := fact.Work()
		switch {
		case errors.Is(err, domain.ErrNoActiveWork):
		case err != nil:
			return err
		default:
			v = visit{active: true, work: work.ID(), stage: work.CurrentStage(), visit: work.CurrentVisit()}
		}
		loaded, ok := observation(in, v)
		if !ok {
			return nil
		}
		stream := eventlog.ObservationsStream(loaded.WorkID)
		return s.Append(stream, history.Version(stream), eventlog.AutomationActor("observeSkillLoads"), loaded)
	})
}
