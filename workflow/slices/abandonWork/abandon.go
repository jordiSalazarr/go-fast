// Package abandonwork lets the owner abandon the active work.
package abandonwork

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what abandoning work needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// AbandonWork abandons the active work.
func AbandonWork(log Log, actor eventlog.Actor, owner domain.Owner, reason domain.Reason) (domain.InProgressWork, error) {
	records, err := log.ReadAll()
	if err != nil {
		return domain.InProgressWork{}, fmt.Errorf("abandon work: %w", err)
	}
	work, version, err := eventlog.NewHistory(records).ActiveWork()
	if err != nil {
		return domain.InProgressWork{}, fmt.Errorf("abandon work: %w", err)
	}
	events, err := work.AbandonWork(owner, reason)
	if err != nil {
		return domain.InProgressWork{}, err
	}
	if err := log.Append(eventlog.WorkStream(work.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return domain.InProgressWork{}, fmt.Errorf("abandon work: %w", err)
	}
	return work, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error)) *cobra.Command {
	return &cobra.Command{
		Use:   `abandon "<reason>"`,
		Short: "Abandon the active work (owner only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reason, err := domain.NewReason(args[0])
			if err != nil {
				return err
			}
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			owner, err := c.RequireOwner("abandon work", `gf abandon "<reason>"`)
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			var work domain.InProgressWork
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automations.AroundCommand(s, func() error {
					work, err = AbandonWork(s, c.Actor(), owner, reason)
					return err
				})
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Abandoned %s work %q on '%s'.\n", work.Type(), work.Description(), work.CurrentStage())
			return nil
		},
	}
}
