// Package briefagentonsessionstart is the SessionStart hook: it briefs Claude
// on the workflow and marks the session's shell as an agent's. On a
// session's startup, with sync configured, it also asks the history server
// which skill improvements are approved for the project, notices those the
// owner declined, and briefs the session to apply the ones the loaded
// plugin directory has not recorded yet (docs/adr/0004).
package briefagentonsessionstart

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/instructions"
	"github.com/jordiSalazarr/go-fast/workflow/pluginskills"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
	"github.com/jordiSalazarr/go-fast/workflow/remote"
	"github.com/jordiSalazarr/go-fast/workflow/skillcatalog"
)

const driveHint = "To drive this work, run /gofast:drive."

const seeLog = "Details are in .gofast/gf.log."

// fetchTimeout bounds the startup check: the server never holds a session
// up for longer.
const fetchTimeout = 3 * time.Second

// sourceStartup is a new session; resume, clear and compact never check
// for skill improvements.
const sourceStartup = "startup"

// agentShell is appended to CLAUDE_ENV_FILE so gf refuses owner-only
// commands from the agent's Bash even if the guard misses one
// (https://code.claude.com/docs/en/hooks "Persist environment variables").
var agentShell = []string{"export " + caller.EnvActor + "=agent"}

// Decline records the applied improvements the owner reverted, in the
// plugin directory's records and the repository's history.
type Decline func(repoRoot, pluginRoot string, store *eventlog.Store) error

// brief is the context Claude gets at session start: the `gf status` text,
// or a note that it could not be read, and how to drive the work.
func brief(statusText string, readable bool) claudehooks.Output {
	if !readable {
		out := claudehooks.Context(claudehooks.EventSessionStart,
			"gofast could not read the workflow state (details are in .gofast/gf.log).\n"+driveHint)
		out.SystemMessage = "gofast could not read the workflow state. Details are in .gofast/gf.log."
		return out
	}
	return claudehooks.Context(claudehooks.EventSessionStart, strings.TrimRight(statusText, "\n")+"\n\n"+driveHint)
}

// NewCommand is the hook. decline and httpClient may be nil: then nothing
// is declined, and the check uses a client with fetchTimeout.
func NewCommand(dir func() string, getenv func(string) string, branchOf func(root string) (domain.Branch, error), decline Decline, httpClient *http.Client) *cobra.Command {
	return &cobra.Command{
		Use:   "session-start",
		Short: "SessionStart hook: brief the agent on the workflow",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			claudehooks.Run(claudehooks.Env{Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Dir: dir(), Getenv: getenv},
				func(repo claudehooks.Repo, in claudehooks.Input) claudehooks.Output {
					if path := repo.Getenv("CLAUDE_ENV_FILE"); path != "" {
						if err := appendLines(path, agentShell); err != nil {
							repo.Logger.Error("could not write CLAUDE_ENV_FILE", "path", path, "error", err.Error())
						}
					}
					var improvements []string
					if in.Source == sourceStartup {
						improvements = checkImprovements(repo, domain.SessionID(in.SessionID), decline, httpClient)
					}
					text, err := statusText(repo, branchOf)
					if err != nil {
						repo.Logger.Error("could not read the workflow state", "error", err.Error())
					}
					return withImprovements(brief(text, err == nil), improvements)
				})
			return nil
		},
	}
}

func statusText(repo claudehooks.Repo, branchOf func(root string) (domain.Branch, error)) (string, error) {
	branch, err := branchOf(repo.Root)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	err = repo.Store.Shared(func(s *eventlog.Snapshot) error {
		view, err := progress.Read(s, branch, progress.FactsAt(repo.Root))
		if err != nil {
			return err
		}
		progress.WarnIfChanged(&view, s)
		progress.RenderText(&text, view)
		return nil
	})
	return text.String(), err
}

func appendLines(path string, lines []string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// exportSession gives the session's shell its id, which `gf improvement`
// acts for. An id a shell would read as more than a word is left out.
func exportSession(repo claudehooks.Repo, session domain.SessionID) {
	path := repo.Getenv("CLAUDE_ENV_FILE")
	if path == "" || session == "" || strings.ContainsAny(string(session), " \t\n'\"$`\\;&|<>()*?[]{}#~") {
		return
	}
	if err := appendLines(path, []string{"export " + pluginskills.SessionEnv + "=" + string(session)}); err != nil {
		repo.Logger.Error("could not write CLAUDE_ENV_FILE", "path", path, "error", err.Error())
	}
}

// withImprovements puts the skill improvements' lines before the rest of
// the brief: they come before anything else.
func withImprovements(out claudehooks.Output, lines []string) claudehooks.Output {
	if len(lines) == 0 || out.HookSpecificOutput == nil {
		return out
	}
	out.HookSpecificOutput.AdditionalContext = strings.Join(lines, "\n") + "\n\n" + out.HookSpecificOutput.AdditionalContext
	return out
}

// checkImprovements is the startup check: without sync configured it does
// nothing; otherwise it declines what the owner reverted, fetches what is
// approved, and says what this session applies. It never fails the hook:
// what goes wrong is one line of the brief, and the log.
func checkImprovements(repo claudehooks.Repo, session domain.SessionID, decline Decline, httpClient *http.Client) []string {
	config, err := remote.LoadConfig(repo.Root)
	if err != nil {
		repo.Logger.Warn("skill improvements: no usable sync configuration", "error", err.Error())
		return nil
	}
	if !config.Configured {
		return nil
	}
	plugin := repo.Getenv(skillcatalog.PluginRootEnv)
	if plugin == "" {
		repo.Logger.Warn("skill improvements: gf doesn't know which plugin directory the session loaded", "env", skillcatalog.PluginRootEnv)
		return nil
	}
	exportSession(repo, session)
	if decline != nil {
		if err := decline(repo.Root, plugin, repo.Store); err != nil {
			repo.Logger.Error("skill improvements: could not record the declined ones", "error", err.Error())
		}
	}
	approved, line := fetch(repo, config, httpClient)
	if line != "" {
		return []string{line}
	}
	local, err := pluginskills.Local(plugin)
	if err != nil {
		repo.Logger.Error("skill improvements: could not read the plugin's skills", "error", err.Error())
		return []string{"skill improvements: gf could not read the skills of " + plugin + "; nothing was applied. " + seeLog}
	}
	if err := pluginskills.SaveFetched(repo.Root, pluginskills.Fetched{PluginRoot: plugin, Improvements: approved}, time.Now()); err != nil {
		repo.Logger.Error("skill improvements: could not keep what was fetched", "error", err.Error())
		return []string{"skill improvements: gf could not keep the approved improvements it fetched; nothing was applied. " + seeLog}
	}
	return improvementLines(plugin, domain.PendingSkillImprovements(approved, local), domain.LeftInProgress(local, session), unreadable(local))
}

// fetch asks the server for the project's approved improvements; line is
// what the brief says when it couldn't.
func fetch(repo claudehooks.Repo, config remote.Config, httpClient *http.Client) ([]domain.ApprovedImprovementFact, string) {
	const nothing = "; nothing was applied"
	secret, err := remote.LoadSecret(repo.Root, repo.Getenv)
	if err != nil || secret.Value == "" {
		repo.Logger.Warn("skill improvements: no project secret", "error", fmt.Sprint(err))
		return nil, "skill improvements: gf has no project secret to ask the history server with" + nothing + ". The owner saves it with `gf sync secret`."
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: fetchTimeout}
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	list, err := remote.NewClient(config, secret.Value, "gf/"+instructions.GfVersion(), httpClient).ApprovedSkillImprovements(ctx)
	var problem *remote.ProblemError
	switch {
	case errors.As(err, &problem):
		repo.Logger.Warn("skill improvements: the history server refused the check", "error", err.Error())
		return nil, "skill improvements: the history server refused the check (" + problem.Problem.Error + ")" + nothing
	case err != nil:
		repo.Logger.Warn("skill improvements: the history server could not be reached", "error", err.Error())
		return nil, "skill improvements: the history server could not be reached" + nothing
	}
	var out []domain.ApprovedImprovementFact
	for _, a := range list.Improvements {
		id, err := domain.ParseSkillImprovementID(a.ID)
		if err != nil || a.Skill == "" || strings.TrimSpace(a.Text) == "" {
			repo.Logger.Warn("skill improvements: skipped an approved improvement gf can't read", "id", a.ID)
			continue
		}
		out = append(out, domain.ApprovedImprovementFact{ID: id, Skill: a.Skill, Text: a.Text, ApprovedAt: a.ApprovedAt})
	}
	return out, ""
}

// unreadable are the record lines gf could not read, by skill.
func unreadable(local []domain.LocalSkill) []string {
	var out []string
	for _, l := range local {
		for _, e := range l.Record {
			if e.Outcome == domain.OutcomeUnreadable {
				out = append(out, l.Name+": "+string(e.Improvement))
			}
		}
	}
	return out
}

// improvementLines brief the session on what to apply now, and on what
// the records keep from being applied.
func improvementLines(plugin string, pending []domain.PendingSkill, left []domain.ImprovingSkill, unreadable []string) []string {
	var lines []string
	if len(pending) > 0 {
		lines = append(lines, "Skill improvements approved on the history server are waiting for the plugin directory "+plugin+", in this order:")
		for _, p := range pending {
			ids := make([]string, len(p.Improvements))
			for i, imp := range p.Improvements {
				ids[i] = string(imp.ID)
			}
			lines = append(lines, "  "+p.Skill+": "+strings.Join(ids, ", "))
		}
		lines = append(lines,
			"Before anything else, without asking the owner, run the gofast:improve-skills agent: it applies them one skill at a time, in this order.",
			"When it finishes, show the owner its summary and the diff of "+plugin+" (e.g. `git -C "+plugin+" diff`), and leave the change uncommitted: the owner commits or reverts it.")
	}
	for _, l := range left {
		lines = append(lines, fmt.Sprintf("skill improvements: %s of %s is still in progress for another session (%s), so %s gets no other improvement. If that session is gone, delete its line from %s to retry it.",
			l.InProgress.Improvement, l.Skill, l.InProgress.Session, l.Skill, pluginskills.RecordPath(plugin, l.Skill)))
	}
	if len(unreadable) > 0 {
		lines = append(lines, "skill improvements: gf could not read these record lines, so they count as recorded and are not applied: "+strings.Join(unreadable, "; ")+".")
	}
	return lines
}
