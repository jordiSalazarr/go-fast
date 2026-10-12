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
	inputs      []WorkInput
}

func (w work) ID() WorkID { return w.id }

// Inputs are the files the owner started the work from, in the order given.
func (w work) Inputs() []WorkInput { return slices.Clone(w.inputs) }

// Input is the work's input of a kind; ok is false when it has none.
func (w work) Input(kind InputKind) (WorkInput, bool) {
	i := slices.IndexFunc(w.inputs, func(in WorkInput) bool { return in.kind == kind })
	if i < 0 {
		return WorkInput{}, false
	}
	return w.inputs[i], true
}

func (w work) hasEventModel() bool      { _, ok := w.Input(InputEventModel); return ok }
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
	Inputs() []WorkInput
	Input(kind InputKind) (WorkInput, bool)
	Path() Path
	CurrentStage() Stage
	CurrentVisit() Visit
	// CurrentStep is the current stage's gate and attempt budget.
	CurrentStep() PathStep
	CurrentAssignment() AssignmentID
	// FirstAssignment is the work's first stage visit: its baseline is the
	// working tree before the work changed anything.
	FirstAssignment() AssignmentID
	// Skips reports whether the work leaves out an optional stage of its
	// path.
	Skips(stage Stage) bool
	Reworks() ReworkCount
	ReworkBudget() ReworkBudget
	// StressDecision is the work's stress decision on its current pass along
	// the path, and the owner who made it (zero when review did); ok is false
	// until one is recorded. A rework clears it.
	StressDecision() (choice StressChoice, owner Owner, ok bool)
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
	stress       *stressDecision // nil until decided on this pass; never mutated
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
func (w active) FirstAssignment() AssignmentID {
	return AssignmentIDFor(w.id, w.firstStep().stage, FirstVisit())
}

func (w active) StressDecision() (StressChoice, Owner, bool) {
	if w.stress == nil {
		return StressChoice{}, Owner{}, false
	}
	return w.stress.choice, w.stress.owner, true
}

// next is the stage the work enters after its current one, skipping the
// optional stages it didn't include; done when there is none. Past the
// stress decider it needs a stress decision.
func (w active) next() (next Stage, done bool, err error) {
	i, _ := w.path.indexOf(w.stage)
	if decider, ok := w.path.indexOf(StressDecider()); ok && i >= decider && w.stress == nil {
		return Stage{}, false, ErrStressUndecided
	}
	for _, step := range w.path.steps[i+1:] {
		if w.includes(step) {
			return step.stage, false, nil
		}
	}
	return Stage{}, true, nil
}

// reviewDecides records review's stress choice, unless the owner already
// decided on this pass: the owner's decision stands, except a skip that the
// change now forces to include.
func (w active) reviewDecides(choice StressChoice) []WorkEvent {
	if s := w.stress; s != nil && !s.owner.isZero() && !(s.choice.decision == StressSkip && len(choice.forcedBy) > 0) {
		return nil
	}
	return []WorkEvent{StressDecided{WorkID: w.id, Decision: choice.decision, Reason: choice.reason, ForcedBy: choice.forcedBy}}
}

// stressStillOpen reports whether the owner may still decide stress
// testing: the work hasn't passed the stage after which it is too late.
func (w active) stressStillOpen() bool {
	i, _ := w.path.indexOf(w.stage)
	closes, ok := w.path.indexOf(stressDecisionCloses)
	return ok && i <= closes
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

// StartWork starts a work on a branch, on the first stage of its type's path
// it enters, unless the branch already has active work. Its inputs are the
// files the owner starts it from: only kinds its type accepts, at most one
// of each; an event model makes it enter event modelling. These rules apply
// at start only: a recorded start replays as it was recorded.
func StartWork(id WorkID, workType WorkType, description Description, branch Branch, fact ActiveWorkFact, inputs ...WorkInput) ([]WorkEvent, error) {
	if err := requirePresent(
		required{"work id", id}, required{"work type", workType}, required{"description", description},
		required{"branch", branch}, required{"active work fact", fact},
	); err != nil {
		return nil, fmt.Errorf("start work: %w", err)
	}
	if fact.branch != branch {
		return nil, fmt.Errorf("start work %s on %s: active work fact is for branch %s: %w", id, branch, fact.branch, ErrMissingValue)
	}
	if a := fact.work; fact.kind == activeSome {
		return nil, fmt.Errorf("start work %s: %w", id, &WorkAlreadyActiveError{
			ID: a.ID(), Type: a.Type(), Description: a.Description(), Branch: a.Branch(),
		})
	}
	path, err := PathFor(workType)
	if err != nil {
		return nil, fmt.Errorf("start work %s: %w", id, err)
	}
	for _, in := range inputs {
		if err := requirePresent(required{"input kind", in.kind}, required{"input path", in.path}, required{"input hash", in.hash}); err != nil {
			return nil, fmt.Errorf("start work %s: work input: %w", id, err)
		}
		if err := workType.CheckInput(in.kind, in.path); err != nil {
			return nil, fmt.Errorf("start work %s: %w", id, err)
		}
	}
	if err := checkInputs(path, inputs); err != nil {
		return nil, fmt.Errorf("start work %s: %w", id, err)
	}
	first := active{work: work{inputs: inputs}, path: path}.firstStep()
	return []WorkEvent{
		WorkStarted{WorkID: id, WorkType: workType, PathVersion: path.version, Description: description, Branch: branch, Inputs: slices.Clone(inputs)},
		StageEntered{WorkID: id, Stage: first.stage, Visit: FirstVisit(), Gate: first.gate, Budget: first.budget},
	}, nil
}

// AdvancePastStage moves the work past its current stage visit: onto a new
// visit of the next stage it includes, or to completion after the last one.
// Advancing past the stress decider (review) records its stress choice,
// which is required there and refused anywhere else.
func (w InProgressWork) AdvancePastStage(stage Stage, visit Visit, stress StressChoice) ([]WorkEvent, error) {
	if err := requirePresent(required{"stage", stage}, required{"visit", visit}); err != nil {
		return nil, fmt.Errorf("advance work %s: %w", w.id, err)
	}
	if err := w.requireCurrent(stage, visit); err != nil {
		return nil, fmt.Errorf("advance work %s: %w", w.id, err)
	}
	var events []WorkEvent
	switch {
	case stage == StressDecider() && stress.isZero():
		return nil, fmt.Errorf("advance work %s past %s: %w", w.id, stage, ErrMissingStressDecision)
	case stage == StressDecider() && !stress.valid():
		return nil, fmt.Errorf("advance work %s past %s: %w", w.id, stage, ErrStressForced)
	case stage == StressDecider():
		events = w.reviewDecides(stress)
		for _, e := range events {
			w.stress = decidedBy(e.(StressDecided))
		}
	case !stress.isZero():
		return nil, fmt.Errorf("advance work %s past %s: %w", w.id, stage, ErrStressOutsideReview)
	}
	next, done, err := w.next()
	if err != nil {
		return nil, fmt.Errorf("advance work %s: %w", w.id, err)
	}
	if done {
		return append(events, WorkCompleted{WorkID: w.id}), nil
	}
	return append(events, w.entering(next)), nil
}

// OverrideStressDecision records the owner's stress decision, which stands
// for the rest of this pass along the path. It is too late once the work has
// passed integration testing.
func (w InProgressWork) OverrideStressDecision(owner Owner, choice StressChoice) ([]WorkEvent, error) {
	if err := requirePresent(required{"owner", owner}, required{"stress choice", choice}); err != nil {
		return nil, fmt.Errorf("override stress decision of work %s: %w", w.id, err)
	}
	if !choice.valid() {
		return nil, fmt.Errorf("override stress decision of work %s: %w", w.id, ErrStressForced)
	}
	if !w.stressStillOpen() {
		return nil, fmt.Errorf("override stress decision of work %s on %s: %w", w.id, w.stage, ErrStressDecisionClosed)
	}
	return []WorkEvent{StressDecided{WorkID: w.id, Decision: choice.decision, Reason: choice.reason, ForcedBy: choice.forcedBy, Owner: owner}}, nil
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
// code, or a stage whose model the code follows (fixerRedoes) when the work
// entered it. Grill is never a route: it needs the owner live; the owner
// sends work back to it with `gf return grill`. In path order.
func (w InFixerWork) RouteTargets() []Stage {
	var targets []Stage
	end, _ := w.path.indexOf(w.from)
	for _, step := range w.path.steps[:end] {
		if !w.includes(step) {
			continue
		}
		if step.stage.WriteScope().CanChangeCode() || step.stage.fixerRedoes() {
			targets = append(targets, step.stage)
		}
	}
	return targets
}

// fixerRedoes reports whether the fixer may send work back to a stage that
// writes artifacts only, because the code follows what it wrote: a wrong
// design (a missing invariant, a wrong aggregate boundary, a missing slice)
// is fixed by redesigning, a wrong event model (a missing scenario,
// rejection or event) by remodelling, and a wrong refactor plan by
// replanning.
func (s Stage) fixerRedoes() bool {
	switch s {
	case StageDesign, StageEventModelling, StagePlan:
		return true
	}
	return false
}

// CheckRoute rejects a target the fixer may not return the work to, naming
// the ones it may.
func (w InFixerWork) CheckRoute(target Stage) error {
	targets := w.RouteTargets()
	if target.isZero() {
		return fmt.Errorf("route work %s: %w", w.id, &RouteMissingError{Allowed: targets})
	}
	if !slices.Contains(targets, target) {
		return fmt.Errorf("route work %s: %w", w.id, &InvalidRouteError{Target: target, Allowed: targets})
	}
	return nil
}

// ReturnToStage ends the fixer by starting a new visit of an earlier stage;
// from there the path continues in order, so every later stage runs again,
// and the next review decides stress testing again.
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

// ReturnTargets are the stages the owner may send the work back to: the
// stages of its path before the current one that the work entered, in path
// order.
func (w InProgressWork) ReturnTargets() []Stage {
	var targets []Stage
	end, _ := w.path.indexOf(w.stage)
	for _, step := range w.path.steps[:end] {
		if _, entered := w.visits[step.stage]; entered {
			targets = append(targets, step.stage)
		}
	}
	return targets
}

// ReturnTo sends the work back, from its current stage visit, to a new visit
// of an earlier stage, whatever state the current visit is in. From there
// the path continues in order, so every later stage runs again, and the next
// review decides stress testing again. It is not a rework: the fixer did not
// send it.
func (w InProgressWork) ReturnTo(owner Owner, target Stage, reason Reason) ([]WorkEvent, error) {
	if err := requirePresent(required{"owner", owner}, required{"stage", target}, required{"reason", reason}); err != nil {
		return nil, fmt.Errorf("return work %s: %w", w.id, err)
	}
	if !slices.Contains(w.ReturnTargets(), target) {
		return nil, fmt.Errorf("return work %s: %w", w.id, &InvalidReturnError{Target: target, Allowed: w.ReturnTargets()})
	}
	entered := w.entering(target)
	return []WorkEvent{
		WorkReturned{WorkID: w.id, Owner: owner, From: w.stage, FromVisit: w.visit, Target: target, Visit: entered.Visit, Reason: reason},
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
	path, err := PathOf(start.WorkType, start.PathVersion)
	if err != nil {
		return nil, fmt.Errorf("rebuild work %s: %w: %w", start.WorkID, ErrInconsistentHistory, err)
	}
	if start.Branch.isZero() {
		return nil, fmt.Errorf("rebuild work %s: started without a branch: %w", start.WorkID, ErrInconsistentHistory)
	}
	if err := checkInputs(path, start.Inputs); err != nil {
		return nil, fmt.Errorf("rebuild work %s: %w: %w", start.WorkID, ErrInconsistentHistory, err)
	}
	w := work{id: start.WorkID, workType: start.WorkType, description: start.Description, branch: start.Branch, inputs: slices.Clone(start.Inputs)}
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
		a := active{work: w, path: path, reworkBudget: DefaultReworkBudget()}
		first := a.firstStep()
		if !ok || entered != (StageEntered{WorkID: w.id, Stage: first.stage, Visit: FirstVisit(), Gate: first.gate, Budget: first.budget}) {
			return nil, nil, false
		}
		return InProgressWork{a.onStage(entered)}, nil, true
	case InProgressWork:
		switch e := e.(type) {
		case StageEntered:
			next, done, err := s.next()
			if err != nil || done || e != s.entering(next) {
				return nil, nil, false
			}
			return InProgressWork{s.onStage(e)}, nil, true
		case WorkCompleted:
			if _, done, err := s.next(); err != nil || !done {
				return nil, nil, false
			}
			return CompletedWork{work: w}, nil, true
		case StressDecided:
			choice := StressChoice{decision: e.Decision, reason: e.Reason, forcedBy: e.ForcedBy}
			byReview := e.Owner.isZero() && s.stage == StressDecider()
			byOwner := !e.Owner.isZero() && s.stressStillOpen()
			if !choice.valid() || !(byReview || byOwner) {
				return nil, nil, false
			}
			s.stress = decidedBy(e)
			return InProgressWork{s.active}, nil, true
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
		case WorkReturned:
			entered := s.entering(e.Target)
			if e.From != s.stage || e.FromVisit != s.visit || !slices.Contains(s.ReturnTargets(), e.Target) ||
				e.Visit != entered.Visit || e.Owner.isZero() || e.Reason.isZero() {
				return nil, nil, false
			}
			back := s.onStage(entered)
			back.stress = nil // the next review decides again
			return InProgressWork{back}, &entered, true
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
			back := s.onStage(entered)
			back.stress = nil // the next review decides again
			return InProgressWork{back}, &entered, true
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
