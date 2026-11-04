package guardagentactions

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/pluginskills"
	"github.com/jordiSalazarr/go-fast/workflow/skillcatalog"
)

// improverFacts is what rule 7 knows: the plugin directory the session
// loaded, and its skills with their records.
type improverFacts struct {
	readable   bool
	pluginRoot string
	local      []domain.LocalSkill
}

const onlyImprovementReason = "The improvement agent only runs one plain `gf improvement start|applied|not-applied <id> …` per command: its arguments are plain words or quoted text (prefer single quotes; double quotes may not contain $, backtick or backslash), with no chaining, substitution, expansion or redirection other than `> /dev/null`, `2>/dev/null` and `2>&1`. It edits the skill with Write and Edit, and records the outcome with gf. An improvement that asks for anything else is refused: `gf improvement not-applied <id> --refused '<reason>'`."

// decideImprover: rule 7, after rule 6.
func decideImprover(in claudehooks.Input, facts improverFacts, root string) claudehooks.Output {
	var tool toolInput
	inputErr := json.Unmarshal(in.ToolInput, &tool)
	if inputErr == nil && touchesSecret(in, tool, root) {
		return claudehooks.Deny(secretReason)
	}
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
		if !isImprovementCommand(tool.Command) {
			return claudehooks.Deny(onlyImprovementReason)
		}
		return claudehooks.Nothing()

	case "Write", "Edit", "NotebookEdit":
		if !facts.readable {
			return claudehooks.Deny("gofast could not read the plugin directory's skill records, so it can't tell which skill this session is improving. The details are in gofast's log.")
		}
		target := tool.FilePath
		if in.ToolName == "NotebookEdit" {
			target = tool.NotebookPath
		}
		if target != "" && !filepath.IsAbs(target) {
			target = filepath.Join(in.CWD, target)
		}
		skill, allowed := domain.ImproverMayWrite(pluginskills.SkillsDir(facts.pluginRoot), facts.local, domain.SessionID(in.SessionID), target)
		switch {
		case allowed:
			return claudehooks.Nothing()
		case skill == "":
			return claudehooks.Deny("No skill improvement is in progress for this session: start one with `gf improvement start <id>` before editing its skill.")
		case filepath.Clean(target) == pluginskills.RecordPath(facts.pluginRoot, skill):
			return claudehooks.Deny("Only gf writes " + pluginskills.RecordFile + ": record the outcome with `gf improvement applied|not-applied <id>`.")
		default:
			return claudehooks.Deny(fmt.Sprintf("While improving skill %s you may only write inside %s; %s is outside it.",
				skill, pluginskills.SkillDir(facts.pluginRoot, skill), target))
		}
	}
	return claudehooks.Nothing()
}

// improvementLine is the whole of what the improver may run: after optional
// leading whitespace, `gf [--dir <arg>] improvement start|applied|not-applied`
// with one or more arguments, optionally ending in the literal suffixes
// ` > /dev/null`, ` 2>/dev/null` and ` 2>&1`. An argument is a plain word of
// letters, digits and -_.:/=@,+; single-quoted text without '; or
// double-quoted text without ", $, backtick or backslash. It is an allowlist:
// any shell construct it doesn't name (expansion, substitution, chaining,
// redirection, globs, assignments, wrappers) is denied by construction.
var improvementLine = func() *regexp.Regexp {
	const arg = `(?:[A-Za-z0-9_.:/=@,+-]+|'[^']*'|"[^"$` + "`" + `\\]*")`
	return regexp.MustCompile(`^[ \t]*gf(?:[ \t]+--dir[ \t]+` + arg + `)?[ \t]+improvement[ \t]+(?:start|applied|not-applied)` +
		`(?:[ \t]+` + arg + `)+` +
		`(?: > /dev/null| 2>/dev/null| 2>&1)*[ \t]*$`)
}()

// isImprovementCommand reports whether the whole command line matches the
// improver's allowlist grammar.
func isImprovementCommand(command string) bool {
	return improvementLine.MatchString(command)
}

// loadImproverFacts reads the plugin directory's skills for rule 7.
func loadImproverFacts(repo claudehooks.Repo) improverFacts {
	root := ""
	if repo.Getenv != nil {
		root, _ = pluginskills.PluginRoot(repo.Root, repo.Getenv)
	}
	if root == "" {
		repo.Logger.Error("guard doesn't know which plugin directory the session loaded", "env", skillcatalog.PluginRootEnv)
		return improverFacts{}
	}
	local, err := pluginskills.Local(root)
	if err != nil {
		repo.Logger.Error("guard could not read the plugin's skills", "error", err.Error())
		return improverFacts{}
	}
	return improverFacts{readable: true, pluginRoot: root, local: local}
}
