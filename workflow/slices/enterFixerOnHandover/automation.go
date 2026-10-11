// Package enterfixeronhandover takes work into the fixer once a stage visit's
// assignment hands it over.
package enterfixeronhandover

import (
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

const Name = "enterFixerOnHandover"

// Log is what this automation needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// workToHandOver decides whether a handover still has to take its work into
// the fixer: only while the work is in progress on the stage visit that
// handed over. Work at its rework limit waits for the owner; once the owner
// allows more rework it is in progress again, and the handover enters the
// fixer.
func workToHandOver(handover domain.AssignmentHandedToFixer, work domain.WorkState) (domain.InProgressWork, bool) {
	w, ok := work.(domain.InProgressWork)
	if !ok || w.CurrentStage() != handover.Stage || w.CurrentVisit() != handover.Visit {
		return domain.InProgressWork{}, false
	}
	return w, true
}

// Run takes into the fixer every work whose current stage visit handed it
// over, and returns how many events it appended. It reads the log once, and
// again only after appending, so every decision sees the latest work.
func Run(log Log) (int, error) {
	records, err := log.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", Name, err)
	}
	history := eventlog.NewHistory(records)
	appended := 0
	for _, r := range records {
		handover, ok := r.Event.(domain.AssignmentHandedToFixer)
		if !ok {
			continue
		}
		work, version, err := history.Work(handover.WorkID)
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		w, ok := workToHandOver(handover, work)
		if !ok {
			continue
		}
		events, err := w.EnterFixer(handover.Stage, handover.Visit, handover.Reason)
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		if err := log.Append(eventlog.WorkStream(w.ID()), version, eventlog.AutomationActor(Name), eventlog.Events(events)...); err != nil {
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
