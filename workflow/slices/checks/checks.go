// Package checks is the `gf checks` query: the repository's exit-check
// configuration as gf will use it, and the checks each stage requires, so the
// owner can verify both before a run.
package checks

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// UncommittedWarning is shown when the configuration differs from HEAD: a
// change to it during a stage is a change outside every write scope.
const UncommittedWarning = "checks.json has uncommitted changes. Commit it before starting work; changing it during a stage fails that attempt's scope check."

func NewCommand(repositoryRoot func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "checks",
		Short: "Show the exit-check configuration and the checks each stage requires",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := repositoryRoot()
			if err != nil {
				return err
			}
			config, err := exitchecks.LoadConfig(root)
			if err != nil {
				return err
			}
			uncommitted, err := worktree.Uncommitted(root, exitchecks.ConfigPath)
			if err != nil {
				return err
			}
			if uncommitted {
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n\n", UncommittedWarning)
			}
			render(cmd.OutOrStdout(), config)
			return nil
		},
	}
}

func render(w io.Writer, config exitchecks.Config) {
	source := exitchecks.ConfigPath
	if !config.Configured {
		source = "defaults; " + exitchecks.ConfigPath + " does not exist"
	}
	fmt.Fprintf(w, "Exit-check configuration (%s):\n", source)
	fmt.Fprintf(w, "  timeout      %s per check command\n", config.Timeout)
	if c := config.Integration; c != nil {
		fmt.Fprintf(w, "  integration  %s (timeout %s)\n", c, c.Timeout)
	} else {
		fmt.Fprintf(w, "  integration  not configured: integration-passes fails until the owner adds one to %s\n", exitchecks.ConfigPath)
	}
	fmt.Fprintln(w, "\nRequired checks per stage:")
	for _, stage := range domain.Stages() {
		names := make([]string, 0, len(stage.RequiredChecks()))
		for _, c := range stage.RequiredChecks() {
			names = append(names, c.String())
		}
		fmt.Fprintf(w, "  %-20s %s\n", stage, strings.Join(names, ", "))
	}
}
