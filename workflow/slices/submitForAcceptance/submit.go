// Package submitforacceptance submits the current stage visit with the
// outcome of its exit check.
package submitforacceptance

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what submitting needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// SubmitForAcceptance submits the active work's current assignment.
func SubmitForAcceptance(log Log, actor eventlog.Actor, outcome domain.ExitCheckOutcome) (domain.Open, []domain.AssignmentEvent, error) {
	records, err := log.ReadAll()
	if err != nil {
		return domain.Open{}, nil, fmt.Errorf("submit for acceptance: %w", err)
	}
	history := eventlog.NewHistory(records)
	work, _, err := history.ActiveWork()
	if err != nil {
		return domain.Open{}, nil, fmt.Errorf("submit for acceptance: %w", err)
	}
	state, version, err := history.Assignment(work.CurrentAssignment())
	if err != nil {
		return domain.Open{}, nil, fmt.Errorf("submit for acceptance: %w", err)
	}
	open, ok := state.(domain.Open)
	if !ok {
		return domain.Open{}, nil, fmt.Errorf("submit for acceptance on %s: assignment is %T: %w", work.CurrentStage(), state, domain.ErrNotAwaitingSubmission)
	}
	events, err := open.SubmitForAcceptance(outcome)
	if err != nil {
		return domain.Open{}, nil, err
	}
	if err := log.Append(eventlog.AssignmentStream(open.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return domain.Open{}, nil, fmt.Errorf("submit for acceptance: %w", err)
	}
	return open, events, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error)) *cobra.Command {
	var passed bool
	var failed string
	cmd := &cobra.Command{
		Use:   `submit (--passed | --failed "<reason>")`,
		Short: "Submit the current stage with the outcome of its exit check",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.NoArgs(cmd, args); err != nil {
				return err
			}
			if passed == cmd.Flags().Changed("failed") {
				return errors.New(`choose exactly one of --passed or --failed "<reason>"`)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			outcome := domain.ExitCheckPassed()
			if !passed {
				reason, err := domain.NewReason(failed)
				if err != nil {
					return err
				}
				outcome = domain.ExitCheckFailed(reason)
			}
			c, err := resolveCaller()
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			var open domain.Open
			var events []domain.AssignmentEvent
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automations.AroundCommand(s, func() error {
					open, events, err = SubmitForAcceptance(s, c.Actor(), outcome)
					return err
				})
			})
			if err != nil {
				return err
			}
			describe(cmd, open, events)
			return nil
		},
	}
	cmd.Flags().BoolVar(&passed, "passed", false, "the exit check passed")
	cmd.Flags().StringVar(&failed, "failed", "", "the exit check failed, with this reason")
	return cmd
}

func describe(cmd *cobra.Command, open domain.Open, events []domain.AssignmentEvent) {
	out := cmd.OutOrStdout()
	stage := open.Stage()
	for _, e := range events {
		switch e := e.(type) {
		case domain.AttemptFailed:
			fmt.Fprintf(out, "Attempt %d on '%s' failed (%d of %d attempts used): %s\n",
				e.Attempt.Int(), stage, e.Attempt.Int(), open.Budget().Int(), e.Reason)
		case domain.AssignmentEscalated:
			fmt.Fprintf(out, "Escalated: budget exhausted on '%s'. The owner can run `gf extend <attempts>` or `gf abandon \"<reason>\"`.\n", stage)
		case domain.SubmittedForApproval:
			fmt.Fprintf(out, "'%s' passed its exit check and waits for the owner: `gf approve` or `gf reject \"<feedback>\"`.\n", stage)
		case domain.AssignmentAccepted:
			fmt.Fprintf(out, "'%s' accepted. Run `gf status` to see what happens next.\n", stage)
		}
	}
}
