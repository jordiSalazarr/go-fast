// Package observeskillloads records, on the work's observations stream,
// the instructions a stage visit works from: each skill it loads, and the
// version of each, so the log tells which text instructed every stage.
//
// Hooks: SubagentStart (a stage agent starts: its definition and the
// skills it preloads), PostToolUse on the Skill tool (a skill loaded
// during the visit). Each version gf reads is kept under
// .gofast/instructions/. It never blocks anything.
package observeskillloads

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/instructions"
	"github.com/jordiSalazarr/go-fast/workflow/skillcatalog"
)

// Name is the automation's name in the log.
const Name = "observeSkillLoads"

// The Skill tool's input, as PostToolUse receives it in tool_input
// (https://code.claude.com/docs/en/hooks "PostToolUse input": tool_name,
// tool_input, tool_response). Its field is pinned to the Skill tool's input
// schema (Claude Code 2.1.286): skill, the skill's name, a plugin's skills
// as "<plugin>:<name>".
type skillInput struct {
	Skill string `json:"skill"`
}

// agentPrefix is how Claude Code names the plugin's agents in agent_type.
const agentPrefix = "gofast:"

// visit is the stage visit the branch's active work is on, if any.
type visit struct {
	active bool
	work   domain.WorkID
	stage  domain.Stage
	visit  domain.Visit
}

// observation decides what a PostToolUse call observed: a skill invoked
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
	return loaded(v, tool.Skill, domain.SkillInvoked), true
}

func loaded(v visit, skill string, load domain.SkillLoad) domain.SkillLoaded {
	name := strings.TrimSpace(skill)
	if catalog, ok := domain.CatalogSkill(skill); ok {
		name = catalog
	}
	return domain.SkillLoaded{WorkID: v.work, Stage: v.stage, Visit: v.visit, Skill: name, Load: load}
}

// plugin reads versions from the gofast plugin and keeps them in the
// repository. Without a root (gf doesn't know where the plugin is) it reads
// nothing.
type plugin struct {
	root string // the plugin's root; empty when unknown
	repo claudehooks.Repo
}

// skillVersion is the version of a skill of the gofast plugin, kept; zero
// for another plugin's skill or one gf can't read.
func (p plugin) skillVersion(skill string) domain.InstructionsVersion {
	if p.root == "" || strings.Contains(skill, ":") {
		return domain.InstructionsVersion{}
	}
	s, err := instructions.Skill(p.root, skill)
	if err != nil {
		p.repo.Logger.Warn("could not read a skill's version", "skill", skill, "error", err.Error())
		return domain.InstructionsVersion{}
	}
	return p.keep(s)
}

func (p plugin) keep(s instructions.Snapshot) domain.InstructionsVersion {
	if err := instructions.Keep(p.repo.Root, s); err != nil {
		p.repo.Logger.Error("could not keep a version of the plugin's instructions", "error", err.Error())
		return domain.InstructionsVersion{} // a version is recorded only with its copy
	}
	return s.Version
}

// agentStart is what a gofast agent starting on the active work's current
// stage visit observes: the agent, with its definition's version, and each
// skill its definition preloads.
func (p plugin) agentStart(in claudehooks.Input, v visit) []domain.Event {
	agent, ok := strings.CutPrefix(in.AgentType, agentPrefix)
	if !ok || agent == "" || !v.active {
		return nil
	}
	started := domain.AgentStarted{
		WorkID: v.work, Stage: v.stage, Visit: v.visit, Agent: in.AgentType, GfVersion: instructions.GfVersion(),
	}
	if p.root == "" {
		p.repo.Logger.Warn("gf doesn't know where the plugin is, so it recorded no versions", "agent", in.AgentType, "env", skillcatalog.PluginRootEnv)
		return []domain.Event{started}
	}
	started.PluginVersion = instructions.PluginVersion(p.root)
	definition, err := instructions.AgentDefinition(p.root, agent)
	if err != nil {
		p.repo.Logger.Warn("could not read an agent's version", "agent", in.AgentType, "error", err.Error())
		return []domain.Event{started}
	}
	started.Version = p.keep(definition.Snapshot)
	events := []domain.Event{started}
	for _, skill := range definition.Preloads {
		preloaded := loaded(v, skill, domain.SkillPreloaded)
		preloaded.Version = p.skillVersion(preloaded.Skill)
		events = append(events, preloaded)
	}
	return events
}

func NewCommands(dir func() string, getenv func(string) string, branchOf func(root string) (domain.Branch, error)) []*cobra.Command {
	hook := func(use, short, what string, observe func(plugin, claudehooks.Input, visit) []domain.Event) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				claudehooks.Run(claudehooks.Env{Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Dir: dir(), Getenv: getenv},
					func(repo claudehooks.Repo, in claudehooks.Input) claudehooks.Output {
						p := plugin{root: getenv(skillcatalog.PluginRootEnv), repo: repo}
						if err := record(repo, in, branchOf, func(v visit) []domain.Event { return observe(p, in, v) }); err != nil {
							repo.Logger.Error("could not record "+what, "error", err.Error())
						}
						return claudehooks.Nothing()
					})
				return nil
			},
		}
	}
	return []*cobra.Command{
		hook("post-tool-use", "PostToolUse hook: record the skills a stage visit loads", "a skill load",
			func(p plugin, in claudehooks.Input, v visit) []domain.Event {
				invoked, ok := observation(in, v)
				if !ok {
					return nil
				}
				invoked.Version = p.skillVersion(invoked.Skill)
				return []domain.Event{invoked}
			}),
		hook("agent-start", "SubagentStart hook: record a stage agent's instructions and their versions", "an agent start",
			func(p plugin, in claudehooks.Input, v visit) []domain.Event { return p.agentStart(in, v) }),
	}
}

// record appends what observe observed on the branch's active work's
// current stage visit.
func record(repo claudehooks.Repo, in claudehooks.Input, branchOf func(root string) (domain.Branch, error), observe func(visit) []domain.Event) error {
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
		work, err := fact.Work()
		if errors.Is(err, domain.ErrNoActiveWork) {
			return nil
		}
		if err != nil {
			return err
		}
		events := observe(visit{active: true, work: work.ID(), stage: work.CurrentStage(), visit: work.CurrentVisit()})
		if len(events) == 0 {
			return nil
		}
		stream := eventlog.ObservationsStream(work.ID())
		return s.Append(stream, history.Version(stream), eventlog.AutomationActor(Name), events...)
	})
}
