// Package startwork starts a new work on the first stage of its path.
package startwork

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jordiSalazarr/go-fast/workflow/automations"
	"github.com/jordiSalazarr/go-fast/workflow/caller"
	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

// Log is what starting work needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// StartWork starts a work on a branch unless the branch has active work.
func StartWork(log Log, actor eventlog.Actor, branch domain.Branch, id domain.WorkID, workType domain.WorkType, description domain.Description) ([]domain.WorkEvent, error) {
	records, err := log.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	active, err := eventlog.NewHistory(records).ActiveWorkOn(branch)
	if err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	events, err := domain.StartWork(id, workType, description, branch, active)
	if err != nil {
		return nil, err
	}
	if err := log.Append(eventlog.WorkStream(id), 0, actor, eventlog.Events(events)...); err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	return events, nil
}

func NewCommand(openStore func() (*eventlog.Store, error), resolveCaller func() (caller.Caller, error), currentBranch func() (domain.Branch, error)) *cobra.Command {
	var typeName string
	cmd := &cobra.Command{
		Use:   `start --type fix-bug "<description>"`,
		Short: "Start work on the first stage of its path",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			workType, err := domain.ParseWorkType(typeName)
			if err != nil {
				return err
			}
			description, err := domain.NewDescription(args[0])
			if err != nil {
				return err
			}
			id, err := newWorkID()
			if err != nil {
				return err
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
			var events []domain.WorkEvent
			err = store.Exclusive(func(s *eventlog.Session) error {
				return automations.AroundCommand(s, func() error {
					events, err = StartWork(s, c.Actor(), branch, id, workType, description)
					return err
				})
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Started %s work %q (%s) on branch %s.\n", workType, description, id, branch)
			for _, e := range events {
				if entered, ok := e.(domain.StageEntered); ok {
					fmt.Fprintf(out, "Current stage: %s. Run `gf status` to see what happens next.\n", entered.Stage)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typeName, "type", "", "work type (available: fix-bug)")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

func newWorkID() (domain.WorkID, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return domain.WorkID{}, fmt.Errorf("generate work id: %w", err)
	}
	return domain.NewWorkID("w-" + hex.EncodeToString(b))
}
