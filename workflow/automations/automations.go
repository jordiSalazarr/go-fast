// Package automations runs the automation slices until nothing is left to do.
package automations

import (
	"errors"
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/slices/advanceWorkOnAcceptance"
	"github.com/jordiSalazarr/go-fast/workflow/slices/cancelAssignmentOnAbandon"
	"github.com/jordiSalazarr/go-fast/workflow/slices/openAssignmentOnStageEntered"
)

// MaxPasses bounds how many passes Run makes before giving up.
const MaxPasses = 10

var ErrDidNotSettle = errors.New("automations did not settle")

// Log is what the automations need from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

type automation func(Log) (int, error)

var all = []automation{
	func(l Log) (int, error) { return openassignmentonstageentered.Run(l) },
	func(l Log) (int, error) { return advanceworkonacceptance.Run(l) },
	func(l Log) (int, error) { return cancelassignmentonabandon.Run(l) },
}

// Run runs every automation until a full pass appends nothing.
func Run(log Log) error {
	for pass := 1; pass <= MaxPasses; pass++ {
		appended := 0
		for _, run := range all {
			n, err := run(log)
			if err != nil {
				return fmt.Errorf("automations pass %d: %w", pass, err)
			}
			appended += n
		}
		if appended == 0 {
			return nil
		}
	}
	return fmt.Errorf("still appending after %d passes: %w", MaxPasses, ErrDidNotSettle)
}

// AroundCommand runs the automations before a command, to reconcile anything
// a crash left half-done, and again after it, to carry out its consequences.
func AroundCommand(log Log, command func() error) error {
	if err := Run(log); err != nil {
		return fmt.Errorf("reconcile before command: %w", err)
	}
	if err := command(); err != nil {
		return err
	}
	if err := Run(log); err != nil {
		return fmt.Errorf("automations after command: %w", err)
	}
	return nil
}
