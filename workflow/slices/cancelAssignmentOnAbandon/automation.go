// Package cancelassignmentonabandon cancels the unfinished assignments of
// abandoned work.
package cancelassignmentonabandon

import (
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

const Name = "cancelAssignmentOnAbandon"

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

// assignmentToCancel decides whether an assignment of abandoned work still
// has to be cancelled, and why.
func assignmentToCancel(abandoned domain.WorkAbandoned, assignment domain.AssignmentState) (cancellable, domain.Reason, bool) {
	c, ok := assignment.(cancellable)
	if !ok {
		return nil, domain.Reason{}, false
	}
	reason, err := domain.NewReason("work abandoned: " + abandoned.Reason.String())
	if err != nil {
		return nil, domain.Reason{}, false
	}
	return c, reason, true
}

// Run cancels every unfinished assignment of abandoned work and returns how
// many events it appended. It reads the log once, and again only after
// appending.
func Run(log Log) (int, error) {
	records, err := log.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", Name, err)
	}
	history := eventlog.NewHistory(records)
	abandoned := map[domain.WorkID]domain.WorkAbandoned{}
	for _, r := range records {
		if e, ok := r.Event.(domain.WorkAbandoned); ok {
			abandoned[e.WorkID] = e
		}
	}
	appended := 0
	for _, r := range records {
		opened, ok := r.Event.(domain.AssignmentOpened)
		if !ok {
			continue
		}
		workAbandoned, ok := abandoned[opened.WorkID]
		if !ok {
			continue
		}
		state, version, err := history.Assignment(opened.AssignmentID)
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		c, reason, ok := assignmentToCancel(workAbandoned, state)
		if !ok {
			continue
		}
		events, err := c.Cancel(reason)
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		if err := log.Append(eventlog.AssignmentStream(opened.AssignmentID), version, eventlog.AutomationActor(Name), eventlog.Events(events)...); err != nil {
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
