// Package reject lets the owner send a submission back with feedback.
package reject

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what rejecting needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// Reject sends the active work's current assignment back with feedback.
func Reject(log Log, actor eventlog.Actor, owner domain.Owner, feedback domain.Feedback) (domain.AwaitingApproval, []domain.AssignmentEvent, error) {
	records, err := log.ReadAll()
	if err != nil {
		return domain.AwaitingApproval{}, nil, fmt.Errorf("reject: %w", err)
	}
	history := eventlog.NewHistory(records)
	work, _, err := history.ActiveWork()
	if err != nil {
		return domain.AwaitingApproval{}, nil, fmt.Errorf("reject: %w", err)
	}
	state, version, err := history.Assignment(work.CurrentAssignment())
	if err != nil {
		return domain.AwaitingApproval{}, nil, fmt.Errorf("reject: %w", err)
	}
	waiting, ok := state.(domain.AwaitingApproval)
	if !ok {
		return domain.AwaitingApproval{}, nil, fmt.Errorf("reject %s: assignment is %T: %w", work.CurrentStage(), state, domain.ErrNotAwaitingApproval)
	}
	events, err := waiting.Reject(owner, feedback)
	if err != nil {
		return domain.AwaitingApproval{}, nil, err
	}
	if err := log.Append(eventlog.AssignmentStream(waiting.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return domain.AwaitingApproval{}, nil, fmt.Errorf("reject: %w", err)
	}
	return waiting, events, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error)) *cobra.Command {
	return &cobra.Command{
		Use:   `reject "<feedback>"`,
		Short: "Send the submission back with feedback; uses one attempt (owner only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			feedback, err := domain.NewFeedback(args[0])
			if err != nil {
				return err
			}
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			owner, err := c.RequireOwner("reject", `gf reject "<feedback>"`)
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			var waiting domain.AwaitingApproval
			var events []domain.AssignmentEvent
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automations.AroundCommand(s, func() error {
					waiting, events, err = Reject(s, c.Actor(), owner, feedback)
					return err
				})
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, e := range events {
				switch e := e.(type) {
				case domain.AssignmentRejected:
					fmt.Fprintf(out, "Rejected '%s' (%d of %d attempts used): %s\n",
						waiting.Stage(), e.Attempt.Int(), waiting.Budget().Int(), e.Feedback)
				case domain.AssignmentEscalated:
					fmt.Fprintf(out, "Escalated: budget exhausted on '%s'. Run `gf extend <attempts>` or `gf abandon \"<reason>\"`.\n", waiting.Stage())
				}
			}
			return nil
		},
	}
}
