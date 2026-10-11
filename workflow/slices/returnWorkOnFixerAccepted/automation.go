// Package returnworkonfixeraccepted returns work to the stage the fixer chose
// once the fixer's stage visit is accepted.
package returnworkonfixeraccepted

import (
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

const Name = "returnWorkOnFixerAccepted"

// Log is what this automation needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// workToReturn decides whether an acceptance still has to return its work:
// only for the fixer, and only while the work is still in that fixer visit.
func workToReturn(accepted domain.AssignmentAccepted, work domain.WorkState) (domain.InFixerWork, bool) {
	if accepted.Stage != domain.StageFixer {
		return domain.InFixerWork{}, false
	}
	w, ok := work.(domain.InFixerWork)
	if !ok || w.CurrentVisit() != accepted.Visit {
		return domain.InFixerWork{}, false
	}
	return w, true
}

// Run returns every work whose current fixer visit was accepted to the
// stage its route names, and returns how many events it appended. It reads
// the log once, and again only after appending.
func Run(log Log) (int, error) {
	records, err := log.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", Name, err)
	}
	history := eventlog.NewHistory(records)
	appended := 0
	for _, r := range records {
		accepted, ok := r.Event.(domain.AssignmentAccepted)
		if !ok || accepted.Stage != domain.StageFixer {
			continue
		}
		work, version, err := history.Work(accepted.WorkID)
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		w, ok := workToReturn(accepted, work)
		if !ok {
			continue
		}
		events, err := w.ReturnToStage(accepted.Route.Target(), accepted.Route.Reason())
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
