// Package extendreworkbudget lets the owner allow more rework of work that
// reached its rework limit.
package extendreworkbudget

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what extending the rework budget needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// ExtendReworkBudget allows the active work more reworks.
func ExtendReworkBudget(log Log, actor eventlog.Actor, branch domain.Branch, owner domain.Owner, additional domain.ReworkBudget) (domain.ReworkBudgetExtended, error) {
	records, err := log.ReadAll()
	if err != nil {
		return domain.ReworkBudgetExtended{}, fmt.Errorf("extend rework budget: %w", err)
	}
	history := eventlog.NewHistory(records)
	fact, err := history.ActiveWorkOn(branch)
	if err != nil {
		return domain.ReworkBudgetExtended{}, fmt.Errorf("extend rework budget: %w", err)
	}
	work, err := fact.Work()
	if err != nil {
		return domain.ReworkBudgetExtended{}, fmt.Errorf("extend rework budget: %w", err)
	}
	limit, ok := work.(domain.ReworkLimitReachedWork)
	if !ok {
		return domain.ReworkBudgetExtended{}, fmt.Errorf("extend rework budget of %s: work is %T: %w", work.ID(), work, domain.ErrNotAtReworkLimit)
	}
	events, err := limit.AllowMoreRework(owner, additional)
	if err != nil {
		return domain.ReworkBudgetExtended{}, err
	}
	version := history.Version(eventlog.WorkStream(work.ID()))
	if err := log.Append(eventlog.WorkStream(work.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return domain.ReworkBudgetExtended{}, fmt.Errorf("extend rework budget: %w", err)
	}
	var extended domain.ReworkBudgetExtended
	for _, e := range events {
		if e, ok := e.(domain.ReworkBudgetExtended); ok {
			extended = e
		}
	}
	return extended, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), automate automations.Runner, resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "rework-extend <reworks>",
		Short: "Allow more rework of work that reached its rework limit (owner only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("reworks %q: %w", args[0], domain.ErrInvalidReworkBudget)
			}
			additional, err := domain.NewReworkBudget(n)
			if err != nil {
				return err
			}
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			owner, err := c.RequireOwner("allow more rework", "gf rework-extend <n>")
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
			var extended domain.ReworkBudgetExtended
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automate.AroundCommand(s, func() error {
					extended, err = ExtendReworkBudget(s, c.Actor(), branch, owner, additional)
					return err
				})
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Extended the rework budget by %d to %d. The work goes to the fixer; run /gofast:drive to continue.\n",
				extended.Additional.Int(), extended.NewBudget.Int())
			return nil
		},
	}
}
