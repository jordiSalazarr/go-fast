package domain

import "fmt"

// PathStep is one stage of a path with its gate and attempt budget. An
// optional step is entered only if the work decided to include it.
type PathStep struct {
	stage    Stage
	gate     Gate
	budget   AttemptBudget
	optional bool
}

func (s PathStep) Stage() Stage          { return s.stage }
func (s PathStep) Gate() Gate            { return s.gate }
func (s PathStep) Budget() AttemptBudget { return s.budget }
func (s PathStep) Optional() bool        { return s.optional }

// Path is the ordered list of stages a work type goes through.
type Path struct{ steps []PathStep }

// paths is domain knowledge, not configuration.
var paths = map[WorkType][]PathStep{
	WorkTypeFixBug: {
		{stage: StageDiscovery, gate: GateHuman, budget: AttemptBudget{n: 3}},
		{stage: StageSpecify, gate: GateHuman, budget: AttemptBudget{n: 3}},
		{stage: StageImplement, gate: GateAuto, budget: AttemptBudget{n: 3}},
		{stage: StageReview, gate: GateAuto, budget: AttemptBudget{n: 3}},
		{stage: StageIntegrationTesting, gate: GateAuto, budget: AttemptBudget{n: 3}},
		{stage: StageStressTesting, gate: GateAuto, budget: AttemptBudget{n: 3}, optional: true},
	},
	WorkTypeNewFeature:      designedPath,
	WorkTypeModifyBehaviour: designedPath,
}

// designedPath is fix-bug's path with design between discovery and specify.
var designedPath = []PathStep{
	{stage: StageDiscovery, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageDesign, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageSpecify, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageImplement, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageReview, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageIntegrationTesting, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageStressTesting, gate: GateAuto, budget: AttemptBudget{n: 3}, optional: true},
}

// fixerStep is the fixer's gate and budget. The fixer is on no path.
var fixerStep = PathStep{stage: StageFixer, gate: GateAuto, budget: AttemptBudget{n: 3}}

// FixerStep is the fixer's gate and attempt budget.
func FixerStep() PathStep { return fixerStep }

// PathFor returns the path of a work type.
func PathFor(t WorkType) (Path, error) {
	steps, ok := paths[t]
	if !ok {
		return Path{}, fmt.Errorf("path for work type %q: %w", t, ErrUnknownWorkType)
	}
	return Path{steps: steps}, nil
}

// Includes reports whether the stage is on the path.
func (p Path) Includes(stage Stage) bool {
	_, ok := p.indexOf(stage)
	return ok
}

// HasDesign reports whether the work type's path starts with a design.
func (t WorkType) HasDesign() bool {
	p, err := PathFor(t)
	return err == nil && p.Includes(StageDesign)
}

// Steps returns the path's steps in order.
func (p Path) Steps() []PathStep { return append([]PathStep(nil), p.steps...) }

func (p Path) step(i int) PathStep { return p.steps[i] }

// indexOf is the position of a stage on the path.
func (p Path) indexOf(stage Stage) (int, bool) {
	for i, s := range p.steps {
		if s.stage == stage {
			return i, true
		}
	}
	return 0, false
}

// stepOf is the step of a stage: on the path, or the fixer's.
func (p Path) stepOf(stage Stage) (PathStep, bool) {
	if stage == StageFixer {
		return fixerStep, true
	}
	i, ok := p.indexOf(stage)
	if !ok {
		return PathStep{}, false
	}
	return p.steps[i], true
}

// StressDecider is the stage whose agent decides whether stress testing
// runs: no work advances past it without a stress decision.
func StressDecider() Stage { return StageReview }

// stressDecisionCloses is the last stage during which the owner may still
// change the stress decision: after it, stress testing runs or the work is
// done.
var stressDecisionCloses = StageIntegrationTesting

// includes reports whether a step is entered, given the work's stress
// decision. Stress testing is the only optional stage.
func (s PathStep) includedBy(stress *stressDecision) bool {
	if !s.optional {
		return true
	}
	return s.stage == StageStressTesting && stress != nil && stress.choice.decision == StressInclude
}
