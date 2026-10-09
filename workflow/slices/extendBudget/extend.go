// Package extendbudget lets the owner give an escalated assignment more attempts.
package extendbudget

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what extending a budget needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// ExtendBudget extends the budget of the active work's escalated assignment.
func ExtendBudget(log Log, actor eventlog.Actor, branch domain.Branch, owner domain.Owner, additional domain.AttemptBudget) (domain.Stage, domain.BudgetExtended, error) {
	records, err := log.ReadAll()
	if err != nil {
		return domain.Stage{}, domain.BudgetExtended{}, fmt.Errorf("extend budget: %w", err)
	}
	history := eventlog.NewHistory(records)
	fact, err := history.ActiveWorkOn(branch)
	if err != nil {
		return domain.Stage{}, domain.BudgetExtended{}, fmt.Errorf("extend budget: %w", err)
	}
	work, err := fact.Work()
	if err != nil {
		return domain.Stage{}, domain.BudgetExtended{}, fmt.Errorf("extend budget: %w", err)
	}
	state, version, err := history.Assignment(work.CurrentAssignment())
	if err != nil {
		return domain.Stage{}, domain.BudgetExtended{}, fmt.Errorf("extend budget: %w", err)
	}
	escalated, ok := state.(domain.Escalated)
	if !ok {
		return domain.Stage{}, domain.BudgetExtended{}, fmt.Errorf("extend budget of %s: assignment is %T: %w", work.CurrentStage(), state, domain.ErrNotEscalated)
	}
	events, err := escalated.ExtendBudget(owner, additional)
	if err != nil {
		return domain.Stage{}, domain.BudgetExtended{}, err
	}
	if err := log.Append(eventlog.AssignmentStream(escalated.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return domain.Stage{}, domain.BudgetExtended{}, fmt.Errorf("extend budget: %w", err)
	}
	var extended domain.BudgetExtended
	for _, e := range events {
		if e, ok := e.(domain.BudgetExtended); ok {
			extended = e
		}
	}
	return escalated.Stage(), extended, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "extend <attempts>",
		Short: "Give an escalated stage more attempts (owner only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("attempts %q: %w", args[0], domain.ErrInvalidAttemptBudget)
			}
			additional, err := domain.NewAttemptBudget(n)
			if err != nil {
				return err
			}
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			owner, err := c.RequireOwner("extend the budget", "gf extend <attempts>")
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
			var stage domain.Stage
			var extended domain.BudgetExtended
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automations.AroundCommand(s, func() error {
					stage, extended, err = ExtendBudget(s, c.Actor(), branch, owner, additional)
					return err
				})
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Extended the budget of '%s' by %d to %d attempts. Continue with `gf submit`.\n",
				stage, extended.Additional.Int(), extended.NewBudget.Int())
			return nil
		},
	}
}
