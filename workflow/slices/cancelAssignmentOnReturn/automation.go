// Package cancelassignmentonreturn cancels the unfinished assignment of the
// stage visit the owner returned the work from.
package cancelassignmentonreturn

import (
	"errors"
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

const Name = "cancelAssignmentOnReturn"

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

// assignmentToCancel decides whether the assignment of the visit the work
// was returned from still has to be cancelled, and why.
func assignmentToCancel(returned domain.WorkReturned, assignment domain.AssignmentState) (cancellable, domain.Reason, bool) {
	c, ok := assignment.(cancellable)
	if !ok {
		return nil, domain.Reason{}, false
	}
	reason, err := domain.NewReason(fmt.Sprintf("work returned to %s: %s", returned.Target, returned.Reason))
	if err != nil {
		return nil, domain.Reason{}, false
	}
	return c, reason, true
}

// Run cancels the unfinished assignment of every stage visit work was
// returned from, and returns how many events it appended. An assignment not
// opened yet is cancelled once it is. It reads the log once, and again only
// after appending.
func Run(log Log) (int, error) {
	records, err := log.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", Name, err)
	}
	history := eventlog.NewHistory(records)
	appended := 0
	for _, r := range records {
		returned, ok := r.Event.(domain.WorkReturned)
		if !ok {
			continue
		}
		id := domain.AssignmentIDFor(returned.WorkID, returned.From, returned.FromVisit)
		state, version, err := history.Assignment(id)
		if errors.Is(err, eventlog.ErrStreamNotFound) {
			continue
		}
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		c, reason, ok := assignmentToCancel(returned, state)
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
