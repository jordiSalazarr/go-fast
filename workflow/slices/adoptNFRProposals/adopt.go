// Package adoptnfrproposals lets the owner adopt the NFRs a design visit
// proposes into the repository's NFRs, while design waits for approval.
// The NFRs are owner config, not a workflow fact: adopting them records
// nothing in the event log.
package adoptnfrproposals

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

// Reader is what finding the proposals needs from the event log.
type Reader interface {
	ReadAll() ([]eventlog.Recorded, error)
}

// Proposals are the NFR proposals of a design visit.
type Proposals struct {
	Assignment domain.AssignmentID
	Visit      domain.Visit
	Path       domain.RepoPath
}

// Find finds the proposals the owner may adopt now: those of the branch's
// design visit waiting for approval. At any other time it fails with
// domain.ErrNFRAdoptionClosed.
func Find(log Reader, branch domain.Branch) (Proposals, error) {
	records, err := log.ReadAll()
	if err != nil {
		return Proposals{}, fmt.Errorf("adopt NFR proposals: %w", err)
	}
	history := eventlog.NewHistory(records)
	fact, err := history.ActiveWorkOn(branch)
	if err != nil {
		return Proposals{}, fmt.Errorf("adopt NFR proposals: %w", err)
	}
	work, err := fact.Work()
	if err != nil {
		return Proposals{}, fmt.Errorf("adopt NFR proposals: %w", err)
	}
	state, _, err := history.Assignment(work.CurrentAssignment())
	if err != nil {
		state = nil // not opened yet: not awaiting approval either
	}
	if err := domain.CanAdoptNFRProposals(state); err != nil {
		return Proposals{}, fmt.Errorf("adopt NFR proposals on %s: %w", work.CurrentStage(), err)
	}
	return Proposals{
		Assignment: work.CurrentAssignment(), Visit: work.CurrentVisit(),
		Path: domain.NFRProposalsFor(work.ID(), work.CurrentVisit()),
	}, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error), repositoryRoot func() (string, error)) *cobra.Command {
	nfrs := &cobra.Command{
		Use:   "nfrs",
		Short: "Manage the repository's NFRs (owner only)",
	}
	var yes bool
	adopt := &cobra.Command{
		Use:   "adopt [--yes]",
		Short: "Adopt the NFRs design proposes into .gofast/nfrs.json (owner only)",
		Long: "Adopt the NFRs the design visit waiting for approval proposes into " + exitchecks.NFRsPath + " (owner only, from a terminal).\n\n" +
			"A proposed NFR with an existing id replaces it; a new id adds one. It shows the changes and asks first, unless --yes.\n" +
			"Commit " + exitchecks.NFRsPath + " before `gf approve`. Nothing is recorded in the event log.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			if _, err := c.RequireOwner("adopt NFR proposals", "gf nfrs adopt"); err != nil {
				return err
			}
			branch, err := currentBranch()
			if err != nil {
				return err
			}
			root, err := repositoryRoot()
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			var proposals Proposals
			if err := store.Shared(func(s *eventlog.Snapshot) error {
				proposals, err = Find(s, branch)
				return err
			}); err != nil {
				return err
			}
			plan, err := exitchecks.PlanNFRAdoption(root, proposals.Path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !plan.Adoption.Pending() {
				fmt.Fprintf(out, "%s already has the NFRs design proposes in %s: nothing to adopt.\n", exitchecks.NFRsPath, proposals.Path)
				return nil
			}
			describe(out, proposals, plan.Adoption)
			if !yes && !confirm(cmd.InOrStdin(), out) {
				fmt.Fprintln(out, "Nothing adopted.")
				return nil
			}
			// The workflow may have moved on while the owner read the changes.
			err = store.Exclusive(func(s *eventlog.Session) error {
				now, err := Find(s, branch)
				if err != nil {
					return err
				}
				if now != proposals {
					return fmt.Errorf("adopt NFR proposals: design visit changed: %w", domain.ErrNFRAdoptionClosed)
				}
				if plan, err = exitchecks.PlanNFRAdoption(root, proposals.Path); err != nil {
					return err
				}
				return plan.Write(root)
			})
			if err != nil {
				return err
			}
			n := len(plan.Adoption.Added()) + len(plan.Adoption.Replaced())
			fmt.Fprintf(out, "Adopted %d NFR %s into %s. Commit it now (`git add %s && git commit`), then run `gf approve`.\n",
				n, plural(n, "change", "changes"), exitchecks.NFRsPath, exitchecks.NFRsPath)
			return nil
		},
	}
	adopt.Flags().BoolVar(&yes, "yes", false, "adopt without asking for confirmation")
	nfrs.AddCommand(adopt)
	return nfrs
}

// describe lists the NFRs the proposals add and replace.
func describe(w io.Writer, p Proposals, a domain.NFRAdoption) {
	fmt.Fprintf(w, "Design (visit %d) proposes these NFR changes (%s):\n", p.Visit.Int(), p.Path)
	for _, n := range a.Added() {
		fmt.Fprintf(w, "  + add %s: %s\n", n.ID(), summary(n))
	}
	for _, r := range a.Replaced() {
		fmt.Fprintf(w, "  ~ replace %s\n      was: %s\n      now: %s\n", r.Old.ID(), summary(r.Old), summary(r.New))
	}
}

// summary is an NFR on one line: its operation, areas, load and targets.
func summary(n domain.NFR) string {
	areas := make([]string, 0, len(n.Areas()))
	for _, a := range n.Areas() {
		areas = append(areas, a.String())
	}
	return fmt.Sprintf("%s (%s), %d vus for %s; %s", n.Operation(), strings.Join(areas, ", "), n.Load().VUs(), n.Load().Duration(), n.Targets().Describe())
}

// confirm asks on the terminal; only "y" or "yes" confirms.
func confirm(in io.Reader, out io.Writer) bool {
	fmt.Fprintf(out, "Adopt them into %s? [y/N] ", exitchecks.NFRsPath)
	line, _ := bufio.NewReader(in).ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
