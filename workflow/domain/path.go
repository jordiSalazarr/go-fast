package domain

import "fmt"

// PathStep is one stage of a path with its gate and attempt budget.
type PathStep struct {
	stage  Stage
	gate   Gate
	budget AttemptBudget
}

func (s PathStep) Stage() Stage          { return s.stage }
func (s PathStep) Gate() Gate            { return s.gate }
func (s PathStep) Budget() AttemptBudget { return s.budget }

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
	},
}

// PathFor returns the path of a work type.
func PathFor(t WorkType) (Path, error) {
	steps, ok := paths[t]
	if !ok {
		return Path{}, fmt.Errorf("path for work type %q: %w", t, ErrUnknownWorkType)
	}
	return Path{steps: steps}, nil
}

// Steps returns the path's steps in order.
func (p Path) Steps() []PathStep { return append([]PathStep(nil), p.steps...) }

func (p Path) step(i int) PathStep { return p.steps[i] }
func (p Path) isLast(i int) bool   { return i == len(p.steps)-1 }
