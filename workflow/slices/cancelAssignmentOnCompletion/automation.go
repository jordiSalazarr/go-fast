// Package cancelassignmentoncompletion cancels the unfinished assignments
// of a completed work: the owner skipping stress testing while the work is
// on it completes the work with that stage visit still open.
package cancelassignmentoncompletion

import (
	"errors"
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

const Name = "cancelAssignmentOnCompletion"

// Log is what this automation needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// cancellable is satisfied by exactly the assignment states that can still be
// cancelled; accepted and cancelled assignments have no Cancel method.
type cancellable interface {
	Cancel(reason domain.Reason) ([]domain.AssignmentEvent, error)
}

var reason = mustReason("work completed")

func mustReason(s string) domain.Reason {
	r, err := domain.NewReason(s)
	if err != nil {
		panic(err)
	}
	return r
}

// Run cancels every unfinished assignment of a completed work, and returns
// how many events it appended. It reads the log once, and again only after
// appending.
func Run(log Log) (int, error) {
	records, err := log.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", Name, err)
	}
	history := eventlog.NewHistory(records)
	appended := 0
	completed := map[domain.WorkID]bool{}
	for _, r := range records {
		if c, ok := r.Event.(domain.WorkCompleted); ok {
			completed[c.WorkID] = true
		}
	}
	for _, r := range records {
		entered, ok := r.Event.(domain.StageEntered)
		if !ok || !completed[entered.WorkID] {
			continue
		}
		id := domain.AssignmentIDFor(entered.WorkID, entered.Stage, entered.Visit)
		state, version, err := history.Assignment(id)
		if errors.Is(err, eventlog.ErrStreamNotFound) {
			continue
		}
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		c, ok := state.(cancellable)
		if !ok {
			continue
		}
		events, err := c.Cancel(reason)
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		if err := log.Append(eventlog.AssignmentStream(id), version, eventlog.AutomationActor(Name), eventlog.Events(events)...); err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		appended += len(events)
		if history, err = reread(log); err != nil {
			return appended, err
		}
	}
	return appended, nil
}

func reread(log Log) (eventlog.History, error) {
	records, err := log.ReadAll()
	if err != nil {
		return eventlog.History{}, fmt.Errorf("%s: %w", Name, err)
	}
	return eventlog.NewHistory(records), nil
}
