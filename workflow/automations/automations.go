// Package automations runs the automation slices until nothing is left to do.
package automations

import (
	"errors"
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/slices/advanceWorkOnAcceptance"
	"github.com/jordiSalazarr/go-fast/workflow/slices/cancelAssignmentOnAbandon"
	"github.com/jordiSalazarr/go-fast/workflow/slices/cancelAssignmentOnReturn"
	"github.com/jordiSalazarr/go-fast/workflow/slices/enterFixerOnHandover"
	"github.com/jordiSalazarr/go-fast/workflow/slices/openAssignmentOnStageEntered"
	"github.com/jordiSalazarr/go-fast/workflow/slices/returnWorkOnFixerAccepted"
)

// MaxPasses bounds how many passes Run makes before giving up.
const MaxPasses = 10

var ErrDidNotSettle = errors.New("automations did not settle")

// Log is what the automations need from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// Runner runs the automations with what they do outside the event log.
type Runner struct {
	// AssignmentOpened is told about every assignment the automations open,
	// after it is appended: gf records the working-tree baseline there.
	AssignmentOpened func(domain.AssignmentID)
	// Boundary is told the history at the stage boundaries the automations
	// see: before they move a work past an accepted stage visit (before the
	// next visit is entered, so before its baseline), and once they settle.
	// gf refreshes the decisions index there. It may be nil.
	Boundary func(eventlog.History)
}

type automation func(Log) (int, error)

func (r Runner) all() []automation {
	return []automation{
		func(l Log) (int, error) { return openassignmentonstageentered.Run(l, r.AssignmentOpened) },
		func(l Log) (int, error) { return advanceworkonacceptance.Run(l, r.Boundary) },
		func(l Log) (int, error) { return enterfixeronhandover.Run(l) },
		func(l Log) (int, error) { return returnworkonfixeraccepted.Run(l) },
		func(l Log) (int, error) { return cancelassignmentonabandon.Run(l) },
		func(l Log) (int, error) { return cancelassignmentonreturn.Run(l) },
	}
}

// Run runs every automation until a full pass appends nothing, then tells
// Boundary the settled history.
func (r Runner) Run(log Log) error {
	for pass := 1; pass <= MaxPasses; pass++ {
		appended := 0
		for _, run := range r.all() {
			n, err := run(log)
			if err != nil {
				return fmt.Errorf("automations pass %d: %w", pass, err)
			}
			appended += n
		}
		if appended > 0 {
			continue
		}
		if r.Boundary != nil {
			records, err := log.ReadAll()
			if err != nil {
				return fmt.Errorf("automations: %w", err)
			}
			r.Boundary(eventlog.NewHistory(records))
		}
		return nil
	}
	return fmt.Errorf("still appending after %d passes: %w", MaxPasses, ErrDidNotSettle)
}

// AroundCommand runs the automations before a command, to reconcile anything
// a crash left half-done, and again after it, to carry out its consequences.
func (r Runner) AroundCommand(log Log, command func() error) error {
	if err := r.Run(log); err != nil {
		return fmt.Errorf("reconcile before command: %w", err)
	}
	if err := command(); err != nil {
		return err
	}
	if err := r.Run(log); err != nil {
		return fmt.Errorf("automations after command: %w", err)
	}
	return nil
}
