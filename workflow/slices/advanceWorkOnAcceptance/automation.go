// Package advanceworkonacceptance moves work past a stage once the stage
// visit's assignment is accepted.
package advanceworkonacceptance

import (
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

const Name = "advanceWorkOnAcceptance"

// Log is what this automation needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// workToAdvance decides whether an acceptance still has to move its work on:
// only while the work is in progress on the accepted stage visit.
func workToAdvance(accepted domain.AssignmentAccepted, work domain.WorkState) (domain.InProgressWork, bool) {
	w, ok := work.(domain.InProgressWork)
	if !ok || w.CurrentStage() != accepted.Stage || w.CurrentVisit() != accepted.Visit {
		return domain.InProgressWork{}, false
	}
	return w, true
}

// Run advances every work whose current stage visit was accepted and returns
// how many events it appended. It reads the log once, and again only after
// appending, so every decision sees the latest work.
func Run(log Log) (int, error) {
	records, err := log.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", Name, err)
	}
	history := eventlog.NewHistory(records)
	appended := 0
	for _, r := range records {
		accepted, ok := r.Event.(domain.AssignmentAccepted)
		if !ok {
			continue
		}
		work, version, err := history.Work(accepted.WorkID)
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		w, ok := workToAdvance(accepted, work)
		if !ok {
			continue
		}
		events, err := w.AdvancePastStage(accepted.Stage, accepted.Visit)
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
