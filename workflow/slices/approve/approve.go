// Package approve lets the owner accept a submission waiting at a human gate.
package approve

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what approving needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// Approve accepts the active work's current assignment.
func Approve(log Log, actor eventlog.Actor, branch domain.Branch, owner domain.Owner) (domain.Stage, error) {
	records, err := log.ReadAll()
	if err != nil {
		return domain.Stage{}, fmt.Errorf("approve: %w", err)
	}
	history := eventlog.NewHistory(records)
	fact, err := history.ActiveWorkOn(branch)
	if err != nil {
		return domain.Stage{}, fmt.Errorf("approve: %w", err)
	}
	work, err := fact.Work()
	if err != nil {
		return domain.Stage{}, fmt.Errorf("approve: %w", err)
	}
	state, version, err := history.Assignment(work.CurrentAssignment())
	if err != nil {
		return domain.Stage{}, fmt.Errorf("approve: %w", err)
	}
	waiting, ok := state.(domain.AwaitingApproval)
	if !ok {
		return domain.Stage{}, fmt.Errorf("approve %s: assignment is %T: %w", work.CurrentStage(), state, domain.ErrNotAwaitingApproval)
	}
	events, err := waiting.Approve(owner)
	if err != nil {
		return domain.Stage{}, err
	}
	if err := log.Append(eventlog.AssignmentStream(waiting.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return domain.Stage{}, fmt.Errorf("approve: %w", err)
	}
	return waiting.Stage(), nil
}

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error)) *cobra.Command {
	return &cobra.Command{
		Use:   "approve",
		Short: "Approve the submission waiting for the owner (owner only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			owner, err := c.RequireOwner("approve", "gf approve")
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
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automations.AroundCommand(s, func() error {
					stage, err = Approve(s, c.Actor(), branch, owner)
					return err
				})
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Approved '%s'. Run `gf status` to see what happens next.\n", stage)
			return nil
		},
	}
}
