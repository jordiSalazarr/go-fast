// Package returnwork lets the owner send the active work back to an earlier
// stage of its path.
package returnwork

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what returning work needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// ReturnWork sends the branch's active work back to a new visit of target.
// Only work in progress on a stage of its path can be returned: not while
// it is in the fixer or at its rework limit.
func ReturnWork(log Log, actor eventlog.Actor, branch domain.Branch, owner domain.Owner, target domain.Stage, reason domain.Reason) (domain.InProgressWork, error) {
	records, err := log.ReadAll()
	if err != nil {
		return domain.InProgressWork{}, fmt.Errorf("return work: %w", err)
	}
	history := eventlog.NewHistory(records)
	fact, err := history.ActiveWorkOn(branch)
	if err != nil {
		return domain.InProgressWork{}, fmt.Errorf("return work: %w", err)
	}
	active, err := fact.Work()
	if err != nil {
		return domain.InProgressWork{}, fmt.Errorf("return work: %w", err)
	}
	work, ok := active.(domain.InProgressWork)
	if !ok {
		return domain.InProgressWork{}, fmt.Errorf("return work %s from %s: %w", active.ID(), active.CurrentStage(), domain.ErrNotReturnable)
	}
	events, err := work.ReturnTo(owner, target, reason)
	if err != nil {
		return domain.InProgressWork{}, err
	}
	version := history.Version(eventlog.WorkStream(work.ID()))
	if err := log.Append(eventlog.WorkStream(work.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return domain.InProgressWork{}, fmt.Errorf("return work: %w", err)
	}
	return work, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), automate automations.Runner, resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error)) *cobra.Command {
	return &cobra.Command{
		Use:   `return <stage> "<reason>"`,
		Short: "Send the active work back to an earlier stage of its path (owner only)",
		Long: "Send the active work back to an earlier stage of its path (owner only).\n\n" +
			"The current stage visit is cancelled and the stage enters a new visit, which reads the reason;\n" +
			"every later stage then runs again. It is not a rework: it uses none of the rework budget.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := domain.ParseStage(args[0])
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
			owner, err := c.RequireOwner("send work back", `gf return <stage> "<reason>"`)
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
			var work domain.InProgressWork
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automate.AroundCommand(s, func() error {
					work, err = ReturnWork(s, c.Actor(), branch, owner, target, reason)
					return err
				})
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Returned %s work %q from '%s' to '%s'. Run /gofast:drive to continue.\n",
				work.Type(), work.Description(), work.CurrentStage(), target)
			return nil
		},
	}
}
