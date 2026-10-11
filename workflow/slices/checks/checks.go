// Package checks is the `gf checks` query: the repository's exit-check
// configuration and NFRs as gf will use them, and the checks each stage
// requires, so the owner can verify them before a run.
package checks

import (
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// UncommittedWarning is shown when the configuration differs from HEAD: a
// change to it during a stage is a change outside every write scope.
const UncommittedWarning = "checks.json has uncommitted changes. Commit it before starting work; changing it during a stage fails that attempt's scope check."

// NFRsUncommittedWarning is UncommittedWarning for the NFRs.
const NFRsUncommittedWarning = "nfrs.json has uncommitted changes. Commit it before starting work; changing it during a stage fails that attempt's scope check."

func NewCommand(repositoryRoot func() (string, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "checks",
		Short: "Show the exit-check configuration, the NFRs and the checks each stage requires",
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
			nfrs, err := exitchecks.LoadNFRs(root)
			if err != nil {
				return err
			}
			for _, owned := range []struct{ path, warning string }{
				{exitchecks.ConfigPath, UncommittedWarning},
				{exitchecks.NFRsPath, NFRsUncommittedWarning},
			} {
				uncommitted, err := worktree.Uncommitted(root, owned.path)
				if err != nil {
					return err
				}
				if uncommitted {
					fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n\n", owned.warning)
				}
			}
			render(cmd.OutOrStdout(), config)
			renderNFRs(cmd.OutOrStdout(), nfrs)
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
	if st := config.Stress; st != nil {
		fmt.Fprintf(w, "  stress       start %s, ready at %s within %s, k6 sends to %s (timeout %s per run)\n",
			strings.Join(st.Start, " "), st.Ready, st.StartTimeout, st.BaseURL, st.Timeout)
	} else {
		fmt.Fprintf(w, "  stress       not configured: stress testing fails until the owner adds a \"stress\" section to %s\n", exitchecks.ConfigPath)
	}
	if k6, err := exec.LookPath("k6"); err == nil {
		fmt.Fprintf(w, "  k6           %s\n", k6)
	} else {
		fmt.Fprintln(w, "  k6           not found on PATH: stress testing fails until it is installed (https://k6.io)")
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

func renderNFRs(w io.Writer, nfrs []domain.NFR) {
	if len(nfrs) == 0 {
		fmt.Fprintf(w, "\nNFRs: none (%s does not exist or lists none): stress testing is never mandatory.\n", exitchecks.NFRsPath)
		return
	}
	fmt.Fprintf(w, "\nNFRs (%s): stress testing is mandatory when a change touches an NFR's areas.\n", exitchecks.NFRsPath)
	for _, n := range nfrs {
		globs := make([]string, 0, len(n.Areas()))
		for _, a := range n.Areas() {
			globs = append(globs, a.String())
		}
		fmt.Fprintf(w, "  %s\n", n.ID())
		if n.Description() != "" {
			fmt.Fprintf(w, "    %s\n", n.Description())
		}
		fmt.Fprintf(w, "    areas      %s\n", strings.Join(globs, ", "))
		fmt.Fprintf(w, "    operation  %s\n", n.Operation())
		fmt.Fprintf(w, "    load       %d vus for %s\n", n.Load().VUs(), n.Load().Duration())
		fmt.Fprintf(w, "    targets    %s\n", n.Targets().Describe())
	}
}
