package domain

import (
	"fmt"
	"slices"
)

// ActiveWorkFact says whether a branch has active work and, if so, which.
// Build it with ActiveWorkOn.
type ActiveWorkFact struct {
	kind   activeKind
	branch Branch
	work   ActiveWork
}

type activeKind int

const (
	activeUnknown activeKind = iota
	activeNone
	activeSome
)

func (f ActiveWorkFact) isZero() bool { return f.kind == activeUnknown }

// Work returns the branch's active work, or ErrNoActiveWork.
func (f ActiveWorkFact) Work() (ActiveWork, error) {
	if f.kind != activeSome {
		return nil, fmt.Errorf("branch %s: %w", f.branch, ErrNoActiveWork)
	}
	return f.work, nil
}

// ActiveWorkOn is the active work fact of a branch. Work that is neither
// completed nor abandoned is active, on its branch only. More than one
// active work on a branch can only come from a bad merge or a hand edit of
// the log.
func ActiveWorkOn(branch Branch, works []WorkState) (ActiveWorkFact, error) {
	if err := requirePresent(required{"branch", branch}); err != nil {
		return ActiveWorkFact{}, fmt.Errorf("active work: %w", err)
	}
	var active []ActiveWork
	for _, w := range works {
		if a, ok := w.(ActiveWork); ok && a.Branch() == branch {
			active = append(active, a)
		}
	}
	switch len(active) {
	case 0:
		return ActiveWorkFact{kind: activeNone, branch: branch}, nil
	case 1:
		return ActiveWorkFact{kind: activeSome, branch: branch, work: active[0]}, nil
	}
	conflict := &ConflictingActiveWorkError{Branch: branch}
	for _, w := range active {
		conflict.Works = append(conflict.Works, w.ID())
	}
	return ActiveWorkFact{}, fmt.Errorf("active work: %w", conflict)
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
	branch      Branch
}

func (w work) ID() WorkID               { return w.id }
func (w work) Type() WorkType           { return w.workType }
func (w work) Description() Description { return w.description }

// Branch is the git branch the work was started on; it never changes.
func (w work) Branch() Branch { return w.branch }

// ActiveWork is work that is neither completed nor abandoned: in progress on
// a stage of its path, in the fixer, or stopped at its rework limit. It is
// always on one stage visit.
type ActiveWork interface {
	WorkState
	Type() WorkType
	Description() Description
	Branch() Branch
	Path() Path
	CurrentStage() Stage
	CurrentVisit() Visit
	// CurrentStep is the current stage's gate and attempt budget.
	CurrentStep() PathStep
	CurrentAssignment() AssignmentID
	Reworks() ReworkCount
	ReworkBudget() ReworkBudget
	AbandonWork(owner Owner, reason Reason) ([]WorkEvent, error)
}

// active is what every active state knows.
type active struct {
	work
	path         Path
	stage        Stage
	visit        Visit
	visits       visits // the last visit of every stage entered
	reworks      ReworkCount
	reworkBudget ReworkBudget
}

func (w active) Path() Path                 { return w.path }
func (w active) CurrentStage() Stage        { return w.stage }
func (w active) CurrentVisit() Visit        { return w.visit }
func (w active) Reworks() ReworkCount       { return w.reworks }
func (w active) ReworkBudget() ReworkBudget { return w.reworkBudget }
func (w active) CurrentStep() PathStep      { s, _ := w.path.stepOf(w.stage); return s }
func (w active) CurrentAssignment() AssignmentID {
	return AssignmentIDFor(w.id, w.stage, w.visit)
}

// LastVisit is the last visit of a stage; ok is false if it was never entered.
func (w active) LastVisit(stage Stage) (Visit, bool) {
	v, ok := w.visits[stage]
	return v, ok
}

// AbandonWork stops the work for good.
func (w active) AbandonWork(owner Owner, reason Reason) ([]WorkEvent, error) {
	if err := requirePresent(required{"owner", owner}, required{"reason", reason}); err != nil {
		return nil, fmt.Errorf("abandon work %s: %w", w.id, err)
	}
	return []WorkEvent{WorkAbandoned{WorkID: w.id, Owner: owner, Reason: reason}}, nil
}

// entering is the StageEntered of the next visit of a stage.
func (w active) entering(stage Stage) StageEntered {
	step, _ := w.path.stepOf(stage)
	return StageEntered{WorkID: w.id, Stage: stage, Visit: w.visits.next(stage), Gate: step.gate, Budget: step.budget}
}

// onStage is the active state after entering a stage visit.
func (w active) onStage(e StageEntered) active {
	w.stage, w.visit, w.visits = e.Stage, e.Visit, w.visits.with(e.Stage, e.Visit)
	return w
}

// visits maps each stage to its last visit. It is copied on write, so states
// never share it.
type visits map[Stage]Visit

func (v visits) next(stage Stage) Visit {
	if last, ok := v[stage]; ok {
		return last.next()
	}
	return FirstVisit()
}

func (v visits) with(stage Stage, visit Visit) visits {
	out := make(visits, len(v)+1)
	for k, x := range v {
		out[k] = x
	}
	out[stage] = visit
	return out
}

// InProgressWork is on a stage of its path.
type InProgressWork struct{ active }

// InFixerWork is in the fixer, handed over by an earlier stage visit.
type InFixerWork struct {
	active
	from      Stage
	fromVisit Visit
	mode      FixerMode
	reason    Reason
}

func (w InFixerWork) From() Stage      { return w.from }
func (w InFixerWork) FromVisit() Visit { return w.fromVisit }
func (w InFixerWork) Mode() FixerMode  { return w.mode }
func (w InFixerWork) Reason() Reason   { return w.reason }

// ReworkLimitReachedWork was handed over by its current stage visit after
// using its whole rework budget. Nothing proceeds until the owner allows
// more rework or abandons it.
type ReworkLimitReachedWork struct{ active }

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

func (InProgressWork) isWorkState()         {}
func (InFixerWork) isWorkState()            {}
func (ReworkLimitReachedWork) isWorkState() {}
func (CompletedWork) isWorkState()          {}
func (AbandonedWork) isWorkState()          {}

// StartWork starts a work on a branch, on the first stage of its type's path,
// unless the branch already has active work.
func StartWork(id WorkID, workType WorkType, description Description, branch Branch, active ActiveWorkFact) ([]WorkEvent, error) {
	if err := requirePresent(
		required{"work id", id}, required{"work type", workType}, required{"description", description},
		required{"branch", branch}, required{"active work fact", active},
	); err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	if active.branch != branch {
		return nil, fmt.Errorf("start work %s on %s: active work fact is for branch %s: %w", id, branch, active.branch, ErrMissingValue)
	}
	if a := active.work; active.kind == activeSome {
		return nil, fmt.Errorf("start work %s: %w", id, &WorkAlreadyActiveError{
			ID: a.ID(), Type: a.Type(), Description: a.Description(), Branch: a.Branch(),
		})
	}
	path, err := PathFor(workType)
	if err != nil {
		return nil, fmt.Errorf("start work %s: %w", id, err)
	}
	first := path.step(0)
	return []WorkEvent{
		WorkStarted{WorkID: id, WorkType: workType, Description: description, Branch: branch},
		StageEntered{WorkID: id, Stage: first.stage, Visit: FirstVisit(), Gate: first.gate, Budget: first.budget},
	}, nil
}

// AdvancePastStage moves the work past its current stage visit: onto a new
// visit of the next stage of the path, or to completion after the last one.
func (w InProgressWork) AdvancePastStage(stage Stage, visit Visit) ([]WorkEvent, error) {
	if err := requirePresent(required{"stage", stage}, required{"visit", visit}); err != nil {
		return nil, fmt.Errorf("advance work %s: %w", w.id, err)
	}
	if err := w.requireCurrent(stage, visit); err != nil {
		return nil, fmt.Errorf("advance work %s: %w", w.id, err)
	}
	i, _ := w.path.indexOf(w.stage)
	if w.path.isLast(i) {
		return []WorkEvent{WorkCompleted{WorkID: w.id}}, nil
	}
	return []WorkEvent{w.entering(w.path.step(i + 1).stage)}, nil
}

// EnterFixer takes the work into the fixer because its current stage visit
// was handed over: the stage checks code it can't change, and the code is
// wrong. The fixer's mode follows from that stage. Once the work has entered
// the fixer as often as its rework budget allows, it stops at the rework
// limit instead.
func (w InProgressWork) EnterFixer(from Stage, fromVisit Visit, reason Reason) ([]WorkEvent, error) {
	if err := requirePresent(required{"stage", from}, required{"visit", fromVisit}, required{"reason", reason}); err != nil {
		return nil, fmt.Errorf("enter fixer for work %s: %w", w.id, err)
	}
	if err := w.requireCurrent(from, fromVisit); err != nil {
		return nil, fmt.Errorf("enter fixer for work %s: %w", w.id, err)
	}
	if !from.ChecksCode() {
		return nil, fmt.Errorf("enter fixer for work %s from %s: %w", w.id, from, ErrCannotEnterFixer)
	}
	if w.reworkBudget.exhaustedBy(w.reworks) {
		return []WorkEvent{ReworkLimitReached{WorkID: w.id, Reworks: w.reworks}}, nil
	}
	fixer := w.entering(StageFixer)
	return []WorkEvent{
		FixerEntered{WorkID: w.id, From: from, FromVisit: fromVisit, Visit: fixer.Visit, Mode: fixerModeFor(from), Reason: reason},
		fixer,
	}, nil
}

func (w InProgressWork) requireCurrent(stage Stage, visit Visit) error {
	if stage != w.stage || visit != w.visit {
		return &NotCurrentStageError{Requested: stage, RequestedVisit: visit, Current: w.stage, CurrentVisit: w.visit}
	}
	return nil
}

// RouteTargets are the stages the fixer may return the work to: on the
// work's path, earlier than the stage that handed over, and able to change
// code. In path order.
func (w InFixerWork) RouteTargets() []Stage {
	var targets []Stage
	end, _ := w.path.indexOf(w.from)
	for _, step := range w.path.steps[:end] {
		if step.stage.WriteScope().CanChangeCode() {
			targets = append(targets, step.stage)
		}
	}
	return targets
}

// CheckRoute rejects a target the fixer may not return the work to, naming
// the ones it may.
func (w InFixerWork) CheckRoute(target Stage) error {
	if err := requirePresent(required{"target stage", target}); err != nil {
		return fmt.Errorf("route work %s: %w", w.id, err)
	}
	targets := w.RouteTargets()
	if !slices.Contains(targets, target) {
		return fmt.Errorf("route work %s: %w", w.id, &InvalidRouteError{Target: target, Allowed: targets})
	}
	return nil
}

// ReturnToStage ends the fixer by starting a new visit of an earlier stage;
// from there the path continues in order, so every later stage runs again.
func (w InFixerWork) ReturnToStage(target Stage, reason Reason) ([]WorkEvent, error) {
	if err := requirePresent(required{"reason", reason}); err != nil {
		return nil, fmt.Errorf("return work %s: %w", w.id, err)
	}
	if err := w.CheckRoute(target); err != nil {
		return nil, err
	}
	entered := w.entering(target)
	return []WorkEvent{
		ReworkStarted{WorkID: w.id, Target: target, Visit: entered.Visit, Reason: reason},
		entered,
	}, nil
}

// AllowMoreRework extends the rework budget. The work is back in progress
// on the stage visit that handed it over, so the handover enters the fixer.
func (w ReworkLimitReachedWork) AllowMoreRework(owner Owner, additional ReworkBudget) ([]WorkEvent, error) {
	if err := requirePresent(required{"owner", owner}, required{"additional reworks", additional}); err != nil {
		return nil, fmt.Errorf("allow more rework of work %s: %w", w.id, err)
	}
	return []WorkEvent{ReworkBudgetExtended{
		WorkID: w.id, Owner: owner, Additional: additional, NewBudget: w.reworkBudget.extendedBy(additional),
	}}, nil
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
	if start.Branch.isZero() {
		return nil, fmt.Errorf("rebuild work %s: started without a branch: %w", start.WorkID, ErrInconsistentHistory)
	}
	w := work{id: start.WorkID, workType: start.WorkType, description: start.Description, branch: start.Branch}
	// The first stage is entered together with WorkStarted; before that the
	// work has no current stage. Entering the fixer and starting a rework
	// are each recorded with the StageEntered they cause, which must follow.
	var state WorkState
	var expected *StageEntered
	for i, e := range history[1:] {
		if e.workID() != w.id {
			return nil, fmt.Errorf("rebuild work %s: event %d belongs to work %s: %w", w.id, i+2, e.workID(), ErrInconsistentHistory)
		}
		if expected != nil {
			if e != WorkEvent(*expected) {
				return nil, fmt.Errorf("rebuild work %s: event %d (%T) is not the %s visit it must enter: %w", w.id, i+2, e, expected.Stage, ErrInconsistentHistory)
			}
			expected = nil
			continue
		}
		next, follow, ok := evolveWork(w, path, state, e)
		if !ok {
			return nil, fmt.Errorf("rebuild work %s: event %d (%T) not possible in state %T: %w", w.id, i+2, e, state, ErrInconsistentHistory)
		}
		state, expected = next, follow
	}
	if state == nil {
		return nil, fmt.Errorf("rebuild work %s: no stage entered: %w", w.id, ErrInconsistentHistory)
	}
	if expected != nil {
		return nil, fmt.Errorf("rebuild work %s: %s visit never entered: %w", w.id, expected.Stage, ErrInconsistentHistory)
	}
	return state, nil
}

// evolveWork applies one event. follow is the StageEntered that must come
// next, if any.
func evolveWork(w work, path Path, state WorkState, e WorkEvent) (next WorkState, follow *StageEntered, ok bool) {
	switch s := state.(type) {
	case nil:
		entered, ok := e.(StageEntered)
		first := path.step(0)
		if !ok || entered != (StageEntered{WorkID: w.id, Stage: first.stage, Visit: FirstVisit(), Gate: first.gate, Budget: first.budget}) {
			return nil, nil, false
		}
		a := active{work: w, path: path, reworkBudget: DefaultReworkBudget()}
		return InProgressWork{a.onStage(entered)}, nil, true
	case InProgressWork:
		i, _ := path.indexOf(s.stage)
		switch e := e.(type) {
		case StageEntered:
			if path.isLast(i) || e != s.entering(path.step(i+1).stage) {
				return nil, nil, false
			}
			return InProgressWork{s.onStage(e)}, nil, true
		case WorkCompleted:
			if !path.isLast(i) {
				return nil, nil, false
			}
			return CompletedWork{work: w}, nil, true
		case WorkAbandoned:
			return abandoned(w, e), nil, true
		case FixerEntered:
			fixer := s.entering(StageFixer)
			if e.From != s.stage || e.FromVisit != s.visit || !e.From.ChecksCode() || s.reworkBudget.exhaustedBy(s.reworks) ||
				e.Visit != fixer.Visit || e.Mode != fixerModeFor(e.From) || e.Reason.isZero() {
				return nil, nil, false
			}
			in := s.onStage(fixer)
			in.reworks = in.reworks.plusOne()
			return InFixerWork{active: in, from: e.From, fromVisit: e.FromVisit, mode: e.Mode, reason: e.Reason}, &fixer, true
		case ReworkLimitReached:
			if !s.stage.ChecksCode() || !s.reworkBudget.exhaustedBy(s.reworks) || e.Reworks != s.reworks {
				return nil, nil, false
			}
			return ReworkLimitReachedWork{s.active}, nil, true
		}
	case InFixerWork:
		switch e := e.(type) {
		case ReworkStarted:
			if s.CheckRoute(e.Target) != nil || e.Reason.isZero() {
				return nil, nil, false
			}
			entered := s.entering(e.Target)
			if e.Visit != entered.Visit {
				return nil, nil, false
			}
			return InProgressWork{s.onStage(entered)}, &entered, true
		case WorkAbandoned:
			return abandoned(w, e), nil, true
		}
	case ReworkLimitReachedWork:
		switch e := e.(type) {
		case ReworkBudgetExtended:
			if e.Additional.isZero() || e.NewBudget != s.reworkBudget.extendedBy(e.Additional) {
				return nil, nil, false
			}
			s.reworkBudget = e.NewBudget
			return InProgressWork{s.active}, nil, true
		case WorkAbandoned:
			return abandoned(w, e), nil, true
		}
	}
	return nil, nil, false
}

func abandoned(w work, e WorkAbandoned) AbandonedWork {
	return AbandonedWork{work: w, owner: e.Owner, reason: e.Reason}
}
