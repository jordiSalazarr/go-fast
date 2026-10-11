// Package overridestressdecision lets the owner decide whether stress
// testing runs, overriding review's decision, while the work hasn't passed
// integration testing. A change that touches an NFR's areas still forces it.
package overridestressdecision

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what overriding the stress decision needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// StressFacts returns the repository's NFRs and the files the work changed
// since it started.
type StressFacts func(work domain.ActiveWork) ([]domain.NFR, domain.ChangedFiles, error)

// OverrideStressDecision records the owner's stress decision for the active
// work.
func OverrideStressDecision(log Log, actor eventlog.Actor, branch domain.Branch, owner domain.Owner, decision domain.StressDecision, reason domain.Reason, facts StressFacts) (domain.StressDecided, error) {
	records, err := log.ReadAll()
	if err != nil {
		return domain.StressDecided{}, fmt.Errorf("override stress decision: %w", err)
	}
	history := eventlog.NewHistory(records)
	fact, err := history.ActiveWorkOn(branch)
	if err != nil {
		return domain.StressDecided{}, fmt.Errorf("override stress decision: %w", err)
	}
	work, err := fact.Work()
	if err != nil {
		return domain.StressDecided{}, fmt.Errorf("override stress decision: %w", err)
	}
	inProgress, ok := work.(domain.InProgressWork)
	if !ok {
		return domain.StressDecided{}, fmt.Errorf("override stress decision of %s: work is %T: %w", work.ID(), work, domain.ErrStressDecisionClosed)
	}
	nfrs, changed, err := facts(work)
	if err != nil {
		return domain.StressDecided{}, fmt.Errorf("override stress decision: %w", err)
	}
	choice, err := domain.ChooseStress(decision, reason, nfrs, changed)
	if err != nil {
		return domain.StressDecided{}, fmt.Errorf("override stress decision: %w", err)
	}
	events, err := inProgress.OverrideStressDecision(owner, choice)
	if err != nil {
		return domain.StressDecided{}, err
	}
	version := history.Version(eventlog.WorkStream(work.ID()))
	if err := log.Append(eventlog.WorkStream(work.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return domain.StressDecided{}, fmt.Errorf("override stress decision: %w", err)
	}
	return events[0].(domain.StressDecided), nil
}

func NewCommand(openStore func() (*eventlog.Store, error), automate automations.Runner, resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error), facts StressFacts) *cobra.Command {
	return &cobra.Command{
		Use:   `stress <include|skip> "<reason>"`,
		Short: "Decide whether stress testing runs, overriding review (owner only)",
		Long: "Decide whether stress testing runs, overriding review's decision (owner only, from a terminal).\n\n" +
			"Allowed until the work has passed integration testing. A change that touches an NFR's areas can't skip it.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			decision, err := domain.ParseStressDecision(args[0])
			if err != nil {
				return err
			}
			reason, err := domain.NewReason(args[1])
			if err != nil {
				return err
			}
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			owner, err := c.RequireOwner("decide whether stress testing runs", `gf stress <include|skip> "<reason>"`)
			if err != nil {
				return err
			}
			branch, err := currentBranch()
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			var decided domain.StressDecided
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automate.AroundCommand(s, func() error {
					decided, err = OverrideStressDecision(s, c.Actor(), branch, owner, decision, reason, facts)
					return err
				})
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if decided.Decision == domain.StressInclude {
				fmt.Fprintf(out, "Stress testing will run after integration testing%s: %s\n", mandatory(decided.ForcedBy), decided.Reason)
			} else {
				fmt.Fprintf(out, "Stress testing is skipped: %s\n", decided.Reason)
			}
			fmt.Fprintln(out, "Your decision stands over review's until a rework sends the work back.")
			return nil
		},
	}
}

func mandatory(ids []domain.NFRID) string {
	if len(ids) == 0 {
		return ""
	}
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = id.String()
	}
	return " (mandatory: " + strings.Join(names, ", ") + ")"
}
