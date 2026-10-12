// Package checks is the `gf checks` query: the repository's exit-check
// configuration and NFRs as gf will use them, and the checks each stage
// requires, so the owner can verify them before a run.
package checks

import (
	"fmt"
	"io"
	"os/exec"
	"slices"
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

// NoMinCoverageWarning is shown while the owner hasn't set
// refactor.minCoverage: a refactor's coverage-sufficient then only reports
// the coverage of the code about to move.
const NoMinCoverageWarning = "No \"refactor\": {\"minCoverage\": <percent>} in " + exitchecks.ConfigPath + ": a refactor's coverage-sufficient only reports the coverage of the code about to move, and never fails."

// NoMinRPSWarning is shown for an NFR without a minRps target.
func NoMinRPSWarning(id domain.NFRID) string {
	return "NFR '" + id.String() + "' has no minRps target: a script that sleeps between requests can keep latency low by sending little load."
}

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
			if !config.Refactor.MinCoverageSet {
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n\n", NoMinCoverageWarning)
			}
			for _, n := range nfrs {
				if n.Targets().Values().MinRPS == nil {
					fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n\n", NoMinRPSWarning(n.ID()))
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
	fmt.Fprintf(w, "  decisions    %s/ (grill's decision documents; gf generates its README.md)\n", config.Decisions.Path())
	if r := config.Refactor; r.MinCoverageSet && r.MinCoverage > 0 {
		fmt.Fprintf(w, "  refactor     each package in a plan's areas needs %v%% statement coverage before its code moves\n", r.MinCoverage)
	} else {
		fmt.Fprintln(w, "  refactor     coverage of a plan's areas is reported only (refactor.minCoverage is 0 or unset)")
	}
	if k6, err := exec.LookPath("k6"); err == nil {
		fmt.Fprintf(w, "  k6           %s\n", k6)
	} else {
		fmt.Fprintln(w, "  k6           not found on PATH: stress testing fails until it is installed (https://k6.io)")
	}
	fmt.Fprintln(w, "\nRequired checks per stage:")
	for _, stage := range domain.Stages() {
		common, extra := stageChecks(stage)
		fmt.Fprintf(w, "  %-20s %s\n", stage, strings.Join(common, ", "))
		for _, e := range extra {
			fmt.Fprintf(w, "  %-20s %s also: %s\n", "", strings.Join(e.types, ", "), strings.Join(e.checks, ", "))
		}
	}
}

// pathChecks are the checks some work types' paths add to a stage.
type pathChecks struct{ types, checks []string }

// stageChecks are the checks a stage requires on every path it is on, and
// those only some paths add. The fixer is on every path.
func stageChecks(stage domain.Stage) (common []string, extra []pathChecks) {
	byType := map[domain.WorkType][]string{}
	var types []domain.WorkType
	for _, t := range domain.WorkTypes() {
		path, err := domain.PathFor(t)
		if err != nil || (stage != domain.StageFixer && !path.Includes(stage)) {
			continue
		}
		types = append(types, t)
		for _, c := range domain.RequiredChecks(stage, path) {
			byType[t] = append(byType[t], c.String())
		}
	}
	if len(types) == 0 {
		return nil, nil
	}
	for _, c := range byType[types[0]] {
		if !slices.ContainsFunc(types, func(t domain.WorkType) bool { return !slices.Contains(byType[t], c) }) {
			common = append(common, c)
		}
	}
	for _, t := range types {
		var own []string
		for _, c := range byType[t] {
			if !slices.Contains(common, c) {
				own = append(own, c)
			}
		}
		if len(own) == 0 {
			continue
		}
		i := slices.IndexFunc(extra, func(e pathChecks) bool { return slices.Equal(e.checks, own) })
		if i < 0 {
			extra = append(extra, pathChecks{checks: own})
			i = len(extra) - 1
		}
		extra[i].types = append(extra[i].types, t.String())
	}
	return common, extra
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
