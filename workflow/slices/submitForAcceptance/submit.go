// Package submitforacceptance submits the current stage visit with the
// agent's claim about its exit check. The files the visit changed, however
// they were written, are checked against the stage's write scope.
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

// Changes returns the files a stage visit changed since its baseline. When
// the visit has no baseline, it takes one now and reports started.
type Changes func(domain.AssignmentID) (changed domain.ChangedFiles, started bool, err error)

// Submission is what submitting did.
type Submission struct {
	Assignment domain.Open
	Events     []domain.AssignmentEvent
	// ScopeCheckStarted: the visit had no baseline, so its write-scope check
	// starts now and this submission was not checked.
	ScopeCheckStarted bool
}

// SubmitForAcceptance submits the active work's current assignment.
func SubmitForAcceptance(log Log, actor eventlog.Actor, branch domain.Branch, claim domain.AgentClaim, changes Changes) (Submission, error) {
	records, err := log.ReadAll()
	if err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	history := eventlog.NewHistory(records)
	fact, err := history.ActiveWorkOn(branch)
	if err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	work, err := fact.Work()
	if err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	state, version, err := history.Assignment(work.CurrentAssignment())
	if err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	open, ok := state.(domain.Open)
	if !ok {
		return Submission{}, fmt.Errorf("submit for acceptance on %s: assignment is %T: %w", work.CurrentStage(), state, domain.ErrNotAwaitingSubmission)
	}
	changed, started, err := changes(open.ID())
	if err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	events, err := open.SubmitForAcceptance(claim, changed)
	if err != nil {
		return Submission{}, err
	}
	if err := log.Append(eventlog.AssignmentStream(open.ID()), version, actor, eventlog.Events(events)...); err != nil {
		return Submission{}, fmt.Errorf("submit for acceptance: %w", err)
	}
	return Submission{Assignment: open, Events: events, ScopeCheckStarted: started}, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), automate automations.Runner, resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error), changes Changes) *cobra.Command {
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
			claim := domain.ClaimPassed()
			if !passed {
				reason, err := domain.NewReason(failed)
				if err != nil {
					return err
				}
				claim = domain.ClaimFailed(reason)
			}
			c, err := resolveCaller()
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
			var submitted Submission
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automate.AroundCommand(s, func() error {
					submitted, err = SubmitForAcceptance(s, c.Actor(), branch, claim, changes)
					return err
				})
			})
			if err != nil {
				return err
			}
			describe(cmd, submitted)
			return nil
		},
	}
	cmd.Flags().BoolVar(&passed, "passed", false, "the exit check passed")
	cmd.Flags().StringVar(&failed, "failed", "", "the exit check failed, with this reason")
	return cmd
}

func describe(cmd *cobra.Command, submitted Submission) {
	out := cmd.OutOrStdout()
	open := submitted.Assignment
	stage := open.Stage()
	if submitted.ScopeCheckStarted {
		fmt.Fprintf(out, "Note: no baseline of the working tree was recorded when '%s' began, so its write-scope check starts now.\n", stage)
	}
	for _, e := range submitted.Events {
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
