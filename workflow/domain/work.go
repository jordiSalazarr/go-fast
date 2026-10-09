package domain

import "fmt"

// ActiveWorkFact says whether the repository has active work and, if so, which.
type ActiveWorkFact struct {
	kind        activeKind
	id          WorkID
	workType    WorkType
	description Description
}

type activeKind int

const (
	activeUnknown activeKind = iota
	activeNone
	activeSome
)

func NoActiveWork() ActiveWorkFact { return ActiveWorkFact{kind: activeNone} }

func ActiveWork(id WorkID, workType WorkType, description Description) ActiveWorkFact {
	return ActiveWorkFact{kind: activeSome, id: id, workType: workType, description: description}
}

func (f ActiveWorkFact) isZero() bool { return f.kind == activeUnknown }

// ActiveWorkAmong derives the active work fact from the repository's works.
func ActiveWorkAmong(works []WorkState) ActiveWorkFact {
	w, err := ActiveWorkIn(works)
	if err != nil {
		return NoActiveWork()
	}
	return ActiveWork(w.id, w.workType, w.description)
}

// ActiveWorkIn returns the repository's active work. Work that is in progress
// is active; completed and abandoned work is not.
func ActiveWorkIn(works []WorkState) (InProgressWork, error) {
	for _, w := range works {
		if p, ok := w.(InProgressWork); ok {
			return p, nil
		}
	}
	return InProgressWork{}, ErrNoActiveWork
}

// WorkState is the state of a Work rebuilt from its events.
type WorkState interface {
	ID() WorkID
	isWorkState()
}

type work struct {
	id          WorkID
	workType    WorkType
	description Description
}

func (w work) ID() WorkID               { return w.id }
func (w work) Type() WorkType           { return w.workType }
func (w work) Description() Description { return w.description }

// InProgressWork is on one stage of its path.
type InProgressWork struct {
	work
	path    Path
	current int
	visit   Visit
}

func (w InProgressWork) Path() Path          { return w.path }
func (w InProgressWork) CurrentStage() Stage { return w.path.step(w.current).stage }
func (w InProgressWork) CurrentVisit() Visit { return w.visit }

// CurrentAssignment is the assignment of the current stage visit.
func (w InProgressWork) CurrentAssignment() AssignmentID {
	return AssignmentIDFor(w.id, w.CurrentStage(), w.visit)
}

// CompletedWork went through its whole path.
type CompletedWork struct{ work }

// AbandonedWork was abandoned by the owner.
type AbandonedWork struct {
	work
	owner  Owner
	reason Reason
}

func (w AbandonedWork) Owner() Owner   { return w.owner }
func (w AbandonedWork) Reason() Reason { return w.reason }

func (InProgressWork) isWorkState() {}
func (CompletedWork) isWorkState()  {}
func (AbandonedWork) isWorkState()  {}

// StartWork starts a work on the first stage of its type's path.
func StartWork(id WorkID, workType WorkType, description Description, active ActiveWorkFact) ([]WorkEvent, error) {
	if err := requirePresent(
		required{"work id", id}, required{"work type", workType},
		required{"description", description}, required{"active work fact", active},
	); err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	if active.kind == activeSome {
		return nil, fmt.Errorf("start work %s: %w", id, &WorkAlreadyActiveError{
			ID: active.id, Type: active.workType, Description: active.description,
		})
	}
	path, err := PathFor(workType)
	if err != nil {
		return nil, fmt.Errorf("start work %s: %w", id, err)
	}
	first := path.step(0)
	return []WorkEvent{
		WorkStarted{WorkID: id, WorkType: workType, Description: description},
		StageEntered{WorkID: id, Stage: first.stage, Visit: FirstVisit(), Gate: first.gate, Budget: first.budget},
	}, nil
}

// AdvancePastStage moves the work past its current stage visit: onto the
// next stage of the path, or to completion after the last one.
func (w InProgressWork) AdvancePastStage(stage Stage, visit Visit) ([]WorkEvent, error) {
	if err := requirePresent(required{"stage", stage}, required{"visit", visit}); err != nil {
		return nil, fmt.Errorf("advance work %s: %w", w.id, err)
	}
	if stage != w.CurrentStage() || visit != w.visit {
		return nil, fmt.Errorf("advance work %s: %w", w.id, &NotCurrentStageError{
			Requested: stage, RequestedVisit: visit, Current: w.CurrentStage(), CurrentVisit: w.visit,
		})
	}
	if w.path.isLast(w.current) {
		return []WorkEvent{WorkCompleted{WorkID: w.id}}, nil
	}
	// Rework does not exist yet, so every stage is entered on its first visit.
	next := w.path.step(w.current + 1)
	return []WorkEvent{
		StageEntered{WorkID: w.id, Stage: next.stage, Visit: FirstVisit(), Gate: next.gate, Budget: next.budget},
	}, nil
}

// AbandonWork stops the work for good.
func (w InProgressWork) AbandonWork(owner Owner, reason Reason) ([]WorkEvent, error) {
	if err := requirePresent(required{"owner", owner}, required{"reason", reason}); err != nil {
		return nil, fmt.Errorf("abandon work %s: %w", w.id, err)
	}
	return []WorkEvent{WorkAbandoned{WorkID: w.id, Owner: owner, Reason: reason}}, nil
}

// RebuildWork rebuilds a work's state from its stream.
func RebuildWork(history []WorkEvent) (WorkState, error) {
	if len(history) == 0 {
		return nil, fmt.Errorf("rebuild work: empty stream: %w", ErrInconsistentHistory)
	}
	start, ok := history[0].(WorkStarted)
	if !ok {
		return nil, fmt.Errorf("rebuild work: first event is %T, want WorkStarted: %w", history[0], ErrInconsistentHistory)
	}
	path, err := PathFor(start.WorkType)
	if err != nil {
		return nil, fmt.Errorf("rebuild work %s: %w: %w", start.WorkID, ErrInconsistentHistory, err)
	}
	w := work{id: start.WorkID, workType: start.WorkType, description: start.Description}
	// The first stage is entered together with WorkStarted; before that the
	// work has no current stage.
	var state WorkState
	for i, e := range history[1:] {
		if e.workID() != w.id {
			return nil, fmt.Errorf("rebuild work %s: event %d belongs to work %s: %w", w.id, i+2, e.workID(), ErrInconsistentHistory)
		}
		next, ok := evolveWork(w, path, state, e)
		if !ok {
			return nil, fmt.Errorf("rebuild work %s: event %d (%T) not possible in state %T: %w", w.id, i+2, e, state, ErrInconsistentHistory)
		}
		state = next
	}
	if state == nil {
		return nil, fmt.Errorf("rebuild work %s: no stage entered: %w", w.id, ErrInconsistentHistory)
	}
	return state, nil
}

func evolveWork(w work, path Path, state WorkState, e WorkEvent) (WorkState, bool) {
	switch s := state.(type) {
	case nil:
		entered, ok := e.(StageEntered)
		if !ok || !enteredStep(path, 0, entered) {
			return nil, false
		}
		return InProgressWork{work: w, path: path, current: 0, visit: entered.Visit}, true
	case InProgressWork:
		switch e := e.(type) {
		case StageEntered:
			if path.isLast(s.current) || !enteredStep(path, s.current+1, e) {
				return nil, false
			}
			return InProgressWork{work: w, path: path, current: s.current + 1, visit: e.Visit}, true
		case WorkCompleted:
			if !path.isLast(s.current) {
				return nil, false
			}
			return CompletedWork{work: w}, true
		case WorkAbandoned:
			return AbandonedWork{work: w, owner: e.Owner, reason: e.Reason}, true
		}
	}
	return nil, false
}

func enteredStep(path Path, i int, e StageEntered) bool {
	step := path.step(i)
	return e.Stage == step.stage && e.Gate == step.gate && e.Budget == step.budget && e.Visit == FirstVisit()
}
