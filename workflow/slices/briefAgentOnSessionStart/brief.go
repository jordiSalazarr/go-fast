// Package briefagentonsessionstart is the SessionStart hook: it briefs Claude
// on the workflow and marks the session's shell as an agent's.
package briefagentonsessionstart

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/claudehooks"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

const driveHint = "To drive this work, run /gofast:drive."

// agentShell is appended to CLAUDE_ENV_FILE so gf refuses owner-only
// commands from the agent's Bash even if the guard misses one
// (https://code.claude.com/docs/en/hooks "Persist environment variables").
var agentShell = []string{"export " + caller.EnvActor + "=agent"}

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

func NewCommand(dir func() string, getenv func(string) string, branchOf func(root string) (domain.Branch, error)) *cobra.Command {
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
					text, err := statusText(repo, branchOf)
					if err != nil {
						repo.Logger.Error("could not read the workflow state", "error", err.Error())
					}
					return brief(text, err == nil)
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
		view, err := progress.Read(s, branch)
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
