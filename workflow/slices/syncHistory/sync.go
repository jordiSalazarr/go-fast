// Package synchistory is `gf sync`: the owner sends the repository's work
// history to the configured history server. It sends data outside this
// machine, so only the owner runs it, from their own terminal.
package synchistory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/instructions"
	"github.com/jordiSalazarr/go-fast/workflow/remote"
)

// Error is a sync that can't start, or didn't finish, said for the owner.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error), repositoryRoot func() (string, error),
	getenv func(string) string, httpClient *http.Client) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "sync [--dry-run]",
		Short: "Send the work history to the history server in .gofast/sync.json (owner only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			if _, err := c.RequireOwner("sync the work history", "gf sync"); err != nil {
				return err
			}
			root, err := repositoryRoot()
			if err != nil {
				return err
			}
			config, err := remote.LoadConfig(root)
			if err != nil {
				return err
			}
			if !config.Configured {
				return &Error{Message: "Sync is not configured: create " + remote.ConfigPath + " with the server's URL and the project's id. `gf checks` shows the result."}
			}
			token := getenv(remote.TokenEnv)
			if token == "" {
				return &Error{Message: fmt.Sprintf("Set $%s to the project's token in your own shell, then run `gf sync` again.", remote.TokenEnv)}
			}
			checks, err := exitchecks.LoadConfig(root)
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			log, err := remote.ReadLog(store)
			if err != nil {
				return err
			}
			gf := instructions.GfVersion()
			server := remote.NewClient(config, token, "gf/"+gf, httpClient)
			report, err := remote.Sync(cmd.Context(), root, config, checks.Decisions, log, server, remote.Options{DryRun: dryRun, Client: gf})
			render(cmd.OutOrStdout(), config, report)
			return explain(err, config)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "ask the server what it holds and list what would be sent, sending nothing")
	return cmd
}

func render(w io.Writer, c remote.Config, r remote.Report) {
	if r.DryRun {
		fmt.Fprintf(w, "Dry run: nothing was sent to %s (project %s). A sync would send:\n", c.Server, c.Project)
		fmt.Fprintf(w, "  events   %d\n", r.Events)
		fmt.Fprintf(w, "  files    %d named, %d to upload (the server holds %d)\n", r.Files, r.Uploaded, r.Held)
	} else {
		fmt.Fprintf(w, "Synced with %s (project %s):\n", c.Server, c.Project)
		fmt.Fprintf(w, "  events   %d sent, %d the server held already\n", r.Events, r.Duplicates)
		fmt.Fprintf(w, "  files    %d named, %d uploaded (the server held %d)\n", r.Files, r.Uploaded, r.Held)
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(w, "Not sent: %s: %s.\n", s.Path, s.Reason)
	}
	for _, x := range r.Refused {
		fmt.Fprintf(w, "Refused: stream %s at version %d: %s.\n", x.Stream, x.Version, x.Reason)
	}
}

// explain turns what stopped a sync into what the owner does next.
func explain(err error, c remote.Config) error {
	var (
		refused     *remote.RefusedError
		problem     *remote.ProblemError
		unreachable *remote.UnreachableError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refused):
		return &Error{Message: fmt.Sprintf("%d streams were not synced; the rest were. A refused stream's history differs from the server's: "+
			"compare this clone's .gofast/events.jsonl with the server's timeline of that work before syncing it again.", len(refused.Refused))}
	case errors.As(err, &problem) && problem.Problem.Error == "unauthorized":
		return &Error{Message: fmt.Sprintf("The history server %s refused the token for project %s. Check $%s.", c.Server, c.Project, remote.TokenEnv)}
	case errors.As(err, &problem):
		return &Error{Message: fmt.Sprintf("The history server %s refused the sync: %s. Run `gf sync` again; it resumes where it stopped.", c.Server, problem.Problem.Message)}
	case errors.As(err, &unreachable):
		return &Error{Message: fmt.Sprintf("gf could not reach the history server %s: %v. Run `gf sync` again; it resumes where it stopped.", c.Server, unreachable.Err)}
	case errors.Is(err, context.Canceled):
		return &Error{Message: "The sync was interrupted. Run `gf sync` again; it resumes where it stopped."}
	}
	return err
}
