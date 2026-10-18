// Package guardagentactions is the PreToolUse hook: it denies agents the
// owner's commands, access to gofast's own files, writes outside the
// current stage's write scope, and catalog skills before the diagnosis.
package guardagentactions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/agentsessions"
	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/skillcatalog"
)

// stageFacts is what the guard knows about the workflow.
type stageFacts struct {
	readable  bool // false when the workflow state could not be read
	active    bool
	work      domain.WorkID
	stage     domain.Stage
	artifact  domain.RepoPath
	decisions domain.DecisionsDir
	// writes is what the visit may write; on a refactor's implement,
	// narrowed to the accepted plan's areas.
	writes domain.Writes
	// exists reports whether a file exists now: writing one that doesn't
	// adds it. Nil means every file exists.
	exists func(domain.RepoPath) bool
	// catalog and diagnosis decide which catalog skills the stage may load.
	catalog   domain.SkillCatalog
	diagnosis domain.Diagnosis
	// frozen are the scripts measure ran for the work's baseline.
	frozen []domain.FrozenScript
}

// withSkills sets the plugin's skill catalog and what the work has
// diagnosed.
func (f stageFacts) withSkills(catalog domain.SkillCatalog, diagnosis domain.Diagnosis) stageFacts {
	f.catalog, f.diagnosis = catalog, diagnosis
	return f
}

func unreadable() stageFacts   { return stageFacts{} }
func noActiveWork() stageFacts { return stageFacts{readable: true} }

func onStage(work domain.WorkID, stage domain.Stage, visit domain.Visit, decisions domain.DecisionsDir) stageFacts {
	writes, _ := domain.WritesOf(stage, domain.Path{}, work, domain.ScopeFacts{Decisions: decisions})
	return stageFacts{readable: true, active: true, work: work, stage: stage, artifact: domain.ArtifactFor(work, stage, visit), decisions: decisions, writes: writes}
}

// withWrites narrows what the visit may write.
func (f stageFacts) withWrites(writes domain.Writes, exists func(domain.RepoPath) bool) stageFacts {
	f.writes, f.exists = writes, exists
	return f
}

func (f stageFacts) added(p domain.RepoPath) bool { return f.exists != nil && !f.exists(p) }

// Tool input fields, per https://code.claude.com/docs/en/hooks "PreToolUse
// input": Bash has command; Write, Edit and Read have file_path;
// NotebookEdit has notebook_path. Skill has skill, the skill's name, with
// a plugin's skills as "<plugin>:<name>" (e.g. "gofast:concurrency"); the
// hooks reference doesn't list Skill's fields, so this is pinned to the
// Skill tool's input schema (Claude Code 2.1.286: skill, args).
type toolInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Skill        string `json:"skill"`
}

// protectedNames are what a Bash command may not mention: text, then how the
// denial names it. The .gofast/ prefixes also catch globs such as
// `.gofast/event?.jsonl`.
var protectedNames = []struct{ text, name string }{
	{"GF_ACTOR", "GF_ACTOR"},
	{"events.jsonl", "events.jsonl"},
	{"events.lock", "events.lock"},
	{"gf.log", "gf.log"},
	{".gofast/event", ".gofast/events.*"},
	{".gofast/gf", ".gofast/gf.*"},
	{".gofast/runtime", ".gofast/runtime/"},
	{".gofast/instructions", ".gofast/instructions/"},
}

const clearEnvironmentReason = "Agents may not clear the environment while gofast is active."

var ownerOnlyReasons = map[string]string{
	"approve":       "Only the owner can approve. Ask the owner to run `gf approve` in their own terminal.",
	"reject":        "Only the owner can reject a submission. Ask the owner to run `gf reject \"<feedback>\"` in their own terminal.",
	"extend":        "Only the owner can extend a budget. Ask the owner to run `gf extend <attempts>` in their own terminal.",
	"rework-extend": "Only the owner can allow more rework. Ask the owner to run `gf rework-extend <n>` in their own terminal.",
	"stress":        "Only the owner can override the stress-testing decision. Review decides it with `gf submit --passed --stress <include|skip> \"<reason>\"`; otherwise ask the owner to run `gf stress <include|skip> \"<reason>\"` in their own terminal.",
	"abandon":       "Only the owner can abandon work. Ask the owner to run `gf abandon \"<reason>\"` in their own terminal.",
	"log":           "Only the owner can accept a changed event log. Ask the owner to run `gf log accept` in their own terminal.",
	"return":        "Only the owner can send work back to an earlier stage. Ask the owner to run `gf return <stage> \"<reason>\"` in their own terminal.",
	"sync":          "Only the owner can sync the work history: it sends data outside this machine. Ask the owner to run `gf sync` in their own terminal.",
	"nfrs":          "Only the owner can adopt NFR proposals. Write them in the design's nfrs.proposed-v<visit>.json and ask the owner to run `gf nfrs adopt` in their own terminal.",
}

// decide is the guard's rule set:
//  1. Bash running an owner-only gf command, or an owner-only subcommand of a
//     program only known at run time: deny, everywhere.
//  2. Bash mentioning GF_ACTOR or gofast's own files: deny, everywhere.
//  3. Bash clearing the environment (env -i, env -u, unset): deny, everywhere.
//  4. Write, Edit or NotebookEdit outside the stage's write scope, or of a
//     script frozen by the work's baseline: deny, for gofast stage agents and
//     for the main session while it drives. gf submit checks the scope again
//     with git, and stress-scripts-valid the frozen scripts, however the
//     files were written.
//  5. Skill loading a skill of the stage's catalog before the work has a
//     diagnosis (SkillCatalog.MayLoad): deny, for the same callers as rule 4;
//     so is a Read of that skill's SKILL.md, on a best-effort basis.
//
// Rules 1 to 3 need no workflow state, so they hold even when it cannot be
// read (fail closed); a Bash call whose input cannot be read is denied too.
// Rule 4 fails open when the state cannot be read, telling the owner.
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
		for _, p := range protectedNames {
			if strings.Contains(tool.Command, p.text) {
				return claudehooks.Deny(fmt.Sprintf("Agents may not touch %s: it belongs to gofast. Use `gf status` to read the workflow.", p.name))
			}
		}
		if clearsEnvironment(tool.Command) {
			return claudehooks.Deny(clearEnvironmentReason)
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
		if frozen, ok := domain.FrozenAt(facts.frozen, p); ok {
			return claudehooks.Deny(frozen.Changed())
		}
		if facts.writes.Allows(p, facts.added(p)) {
			return claudehooks.Nothing()
		}
		return claudehooks.Deny(scopeReason(facts, p))

	case "Skill":
		if inputErr != nil || !scopeApplies(in, driving) || !facts.active {
			return claudehooks.Nothing()
		}
		if name, ok := domain.CatalogSkill(tool.Skill); ok {
			if err := facts.catalog.MayLoad(name, facts.diagnosis); err != nil {
				return claudehooks.Deny(err.Error())
			}
		}
		return claudehooks.Nothing()

	case "Read":
		if inputErr != nil || !scopeApplies(in, driving) || !facts.active {
			return claudehooks.Nothing()
		}
		target := filepath.ToSlash(tool.FilePath)
		for _, name := range facts.catalog.Skills(facts.diagnosis.Stage) {
			file := skillcatalog.SkillFile(name)
			if target == file || strings.HasSuffix(target, "/"+file) {
				if err := facts.catalog.MayLoad(name, facts.diagnosis); err != nil {
					return claudehooks.Deny(err.Error())
				}
			}
		}
		return claudehooks.Nothing()
	}
	return claudehooks.Nothing()
}

// scopeApplies: rule 4 covers gofast stage agents, and the main session while
// it drives work.
func scopeApplies(in claudehooks.Input, driving bool) bool {
	if strings.HasPrefix(in.AgentType, "gofast:") {
		return true
	}
	return !in.InSubagent() && driving
}

func scopeReason(facts stageFacts, p domain.RepoPath) string {
	if p == facts.decisions.Index() {
		return fmt.Sprintf("%s is generated by gf from the accepted grills; nobody else writes it.", p)
	}
	if areas := facts.writes.Areas(); len(areas) > 0 && facts.stage.WriteScope().Allows(p, facts.work, facts.decisions) {
		names := make([]string, len(areas))
		for i, a := range areas {
			names[i] = a.String()
		}
		return fmt.Sprintf("During '%s' of a refactor you may only change the accepted plan's areas (%s) and add new files under their directories; %s is outside them. If the plan is wrong, say so in your artifact and submit --failed.",
			facts.stage, strings.Join(names, ", "), p)
	}
	switch facts.stage.WriteScope() {
	case domain.WriteScopeArtifactsAndDecision:
		return fmt.Sprintf("During '%s' you may only write the stage artifacts in `%s/`, this work's decision document, `%s/<YYYY-MM-DD>-%s-<slug>.md`, glossaries (`GLOSSARY.md`, `GLOSSARY-MAP.md`) and ADRs (`.md` files under a `docs/adr/` directory).",
			facts.stage, domain.ArtifactDir(facts.work), facts.decisions.Path(), facts.work)
	case domain.WriteScopeArtifacts:
		if facts.stage == domain.StageStressTesting {
			return fmt.Sprintf("During '%s' you may only write the stage artifact `%s` and k6 scripts in `%s/`.",
				facts.stage, facts.artifact, domain.StressScriptsDir(facts.work))
		}
		return fmt.Sprintf("During '%s' you may only write the stage artifact `%s`.", facts.stage, facts.artifact)
	case domain.WriteScopeTestsAndArtifacts:
		return fmt.Sprintf("During '%s' you may only write tests and the stage artifact `%s`.", facts.stage, facts.artifact)
	case domain.WriteScopeTestsSkeletonsAndArtifacts:
		return fmt.Sprintf("During '%s' you may only write tests, new skeleton files (non-test .go files that only declare) and the stage artifact `%s`.", facts.stage, facts.artifact)
	default:
		return fmt.Sprintf("During '%s' you may write anything except gofast's own files in .gofast/ (%s); the stage artifact is `%s`.", facts.stage, p, facts.artifact)
	}
}

func NewCommand(dir func() string, getenv func(string) string, branchOf func(root string) (domain.Branch, error)) *cobra.Command {
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
					out := decide(in, loadFacts(repo, branchOf), driving, repo.Root)
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

func loadFacts(repo claudehooks.Repo, branchOf func(root string) (domain.Branch, error)) stageFacts {
	branch, err := branchOf(repo.Root)
	if err != nil {
		repo.Logger.Error("guard could not tell the current branch", "error", err.Error())
		return unreadable()
	}
	config, err := exitchecks.LoadConfig(repo.Root)
	if err != nil {
		repo.Logger.Error("guard could not read the check configuration", "error", err.Error())
		return unreadable()
	}
	var facts stageFacts
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
			facts = noActiveWork()
			return nil
		}
		if err != nil {
			return err
		}
		facts = onStage(work.ID(), work.CurrentStage(), work.CurrentVisit(), config.Decisions)
		scope := domain.ScopeFacts{Decisions: config.Decisions}
		if work.CurrentStage() == domain.StageImplement && work.Path().Includes(domain.StagePlan) {
			_, end, visit := history.LatestAccepted(work.ID(), domain.StagePlan)
			if visit != (domain.Visit{}) {
				plan, err := exitchecks.AcceptedRefactorPlan(repo.Root, work.ID(), visit, end)
				if err != nil {
					return err
				}
				scope.Plan = &plan
			}
		}
		writes, err := domain.WritesOf(work.CurrentStage(), work.Path(), work.ID(), scope)
		if err != nil {
			return err
		}
		facts.frozen = work.FrozenScripts()
		facts = facts.withWrites(writes, func(p domain.RepoPath) bool {
			_, err := os.Stat(filepath.Join(repo.Root, filepath.FromSlash(p.String())))
			return err == nil
		})
		catalog := loadCatalog(repo)
		if len(catalog.Skills(work.CurrentStage())) == 0 {
			return nil
		}
		measured, _, _ := history.LatestAccepted(work.ID(), domain.StageMeasure)
		bottleneck := false
		if _, inFixer := work.(domain.InFixerWork); inFixer {
			content, err := os.ReadFile(filepath.Join(repo.Root, filepath.FromSlash(facts.artifact.String())))
			bottleneck = err == nil && exitchecks.HasBottleneck(string(content))
		}
		facts = facts.withSkills(catalog, domain.DiagnosisOf(work, measured != (domain.AssignmentID{}), bottleneck))
		return nil
	})
	if err != nil {
		repo.Logger.Error("guard could not read the workflow state", "error", err.Error())
		return unreadable()
	}
	return facts
}

// loadCatalog reads the plugin's skill catalog. Without a plugin root, or
// with a catalog gf can't read, no skill is locked; problems are logged and
// `gf checks` reports them.
func loadCatalog(repo claudehooks.Repo) domain.SkillCatalog {
	empty, _ := domain.NewSkillCatalog(nil)
	root := ""
	if repo.Getenv != nil {
		root = repo.Getenv(skillcatalog.PluginRootEnv)
	}
	if root == "" {
		return empty
	}
	catalog, problems, err := skillcatalog.Load(root)
	if err != nil {
		repo.Logger.Error("guard could not read the skill catalog", "error", err.Error())
		return empty
	}
	for _, p := range problems {
		repo.Logger.Warn("skill catalog", "problem", p)
	}
	return catalog
}
