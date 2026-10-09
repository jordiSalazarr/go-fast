// Package openassignmentonstageentered opens the assignment for every stage
// visit that was entered.
package openassignmentonstageentered

import (
	"fmt"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
)

const Name = "openAssignmentOnStageEntered"

// Log is what this automation needs from the event log.
type Log interface {
	ReadAll() ([]eventlog.Recorded, error)
	Append(stream eventlog.Stream, expectedVersion int, actor eventlog.Actor, events ...domain.Event) error
}

// openAssignment is the command this automation issues.
type openAssignment struct {
	work   domain.WorkID
	stage  domain.Stage
	visit  domain.Visit
	gate   domain.Gate
	budget domain.AttemptBudget
}

// assignmentToOpen decides whether an entered stage visit still needs its
// assignment opened.
func assignmentToOpen(entered domain.StageEntered, alreadyOpened bool) (openAssignment, bool) {
	if alreadyOpened {
		return openAssignment{}, false
	}
	return openAssignment{work: entered.WorkID, stage: entered.Stage, visit: entered.Visit, gate: entered.Gate, budget: entered.Budget}, true
}

// Opened is told about each assignment Run opens, after it is appended. It
// is a side effect outside the log: gf records the stage visit's baseline of
// the working tree there. It may be nil.
type Opened func(domain.AssignmentID)

// Run opens every missing assignment and returns how many events it appended.
// It reads the log once, and again only after appending.
func Run(log Log, opened Opened) (int, error) {
	records, err := log.ReadAll()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", Name, err)
	}
	history := eventlog.NewHistory(records)
	appended := 0
	for _, r := range records {
		entered, ok := r.Event.(domain.StageEntered)
		if !ok {
			continue
		}
		stream := eventlog.AssignmentStream(domain.AssignmentIDFor(entered.WorkID, entered.Stage, entered.Visit))
		cmd, ok := assignmentToOpen(entered, history.Version(stream) > 0)
		if !ok {
			continue
		}
		events, err := domain.OpenAssignment(cmd.work, cmd.stage, cmd.visit, cmd.gate, cmd.budget)
		if err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		if err := log.Append(stream, 0, eventlog.AutomationActor(Name), eventlog.Events(events)...); err != nil {
			return appended, fmt.Errorf("%s: %w", Name, err)
		}
		appended += len(events)
		if opened != nil {
			opened(domain.AssignmentIDFor(cmd.work, cmd.stage, cmd.visit))
		}
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
