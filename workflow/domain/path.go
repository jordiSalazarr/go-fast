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

// PathVersion is the version of a work type's path. A work keeps the path
// it started with, so changing a work type's path never changes work in
// progress. The zero version is v1: works started before paths had
// versions.
type PathVersion struct{ n int }

var (
	PathV1 = PathVersion{n: 1}
	// PathV2 starts feature work from the owner's input: an optional event
	// model, then a live grilling session, then discovery.
	PathV2 = PathVersion{n: 2}
)

// NewPathVersion restores a recorded version; 0 is a work started before
// paths had versions, which follows v1.
func NewPathVersion(n int) (PathVersion, error) {
	if n < 0 || n > PathV2.n {
		return PathVersion{}, fmt.Errorf("path version %d: %w", n, ErrUnknownPathVersion)
	}
	return PathVersion{n: n}, nil
}

func (v PathVersion) Int() int       { return v.n }
func (v PathVersion) String() string { return fmt.Sprintf("v%d", v.orV1().n) }

func (v PathVersion) orV1() PathVersion {
	if v.n == 0 {
		return PathV1
	}
	return v
}

// Path is the ordered list of stages a work type goes through, in one
// version.
type Path struct {
	workType WorkType
	version  PathVersion
	steps    []PathStep
}

// paths is domain knowledge, not configuration. A version, once works can
// start on it, never changes.
var paths = map[WorkType]map[PathVersion][]PathStep{
	WorkTypeFixBug: {PathV1: {
		{stage: StageDiscovery, gate: GateHuman, budget: AttemptBudget{n: 3}},
		{stage: StageSpecify, gate: GateHuman, budget: AttemptBudget{n: 3}},
		{stage: StageImplement, gate: GateAuto, budget: AttemptBudget{n: 3}},
		{stage: StageReview, gate: GateAuto, budget: AttemptBudget{n: 3}},
		{stage: StageIntegrationTesting, gate: GateAuto, budget: AttemptBudget{n: 3}},
		{stage: StageStressTesting, gate: GateAuto, budget: AttemptBudget{n: 3}, optional: true},
	}},
	WorkTypeNewFeature:         {PathV1: designedPath, PathV2: featurePath},
	WorkTypeModifyBehaviour:    {PathV1: designedPath, PathV2: featurePath},
	WorkTypeRefactor:           {PathV1: refactorPath},
	WorkTypeImprovePerformance: {PathV1: performancePath},
}

// currentVersions are the versions new works start on.
var currentVersions = map[WorkType]PathVersion{
	WorkTypeFixBug:             PathV1,
	WorkTypeNewFeature:         PathV2,
	WorkTypeModifyBehaviour:    PathV2,
	WorkTypeRefactor:           PathV1,
	WorkTypeImprovePerformance: PathV1,
}

// designedPath (v1) is fix-bug's path with design between discovery and
// specify.
var designedPath = []PathStep{
	{stage: StageDiscovery, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageDesign, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageSpecify, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageImplement, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageReview, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageIntegrationTesting, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageStressTesting, gate: GateAuto, budget: AttemptBudget{n: 3}, optional: true},
}

// featurePath (v2) starts from the owner's input: event modelling when the
// work has an event model, then grill with the owner, whose approval is the
// owner's approval of the plan; only then discovery, of how the agreed
// behaviour fits the code.
var featurePath = []PathStep{
	{stage: StageEventModelling, gate: GateAuto, budget: AttemptBudget{n: 3}, optional: true},
	{stage: StageGrill, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageDiscovery, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageDesign, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageSpecify, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageImplement, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageReview, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageIntegrationTesting, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageStressTesting, gate: GateAuto, budget: AttemptBudget{n: 3}, optional: true},
}

// refactorPath maps the code about to move first, then settles with the
// owner, live, why it moves and what must not change; the owner approves a
// plan that follows those decisions; characterize pins the behaviour of the
// planned areas with passing tests; only then does the code move, within the
// plan's areas and without touching any existing test.
var refactorPath = []PathStep{
	{stage: StageDiscovery, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageGrill, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StagePlan, gate: GateHuman, budget: AttemptBudget{n: 3}},
	{stage: StageCharacterize, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageImplement, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageReview, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageIntegrationTesting, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageStressTesting, gate: GateAuto, budget: AttemptBudget{n: 3}, optional: true},
}

// performancePath has no grill: the owner says what "improved" means by
// choosing the NFRs the work targets. Measure records their baseline and
// diagnoses the bottleneck; optimize changes the code; stress testing always
// runs, and passes only when the targets are met.
var performancePath = []PathStep{
	{stage: StageDiscovery, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageMeasure, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageOptimize, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageReview, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageIntegrationTesting, gate: GateAuto, budget: AttemptBudget{n: 3}},
	{stage: StageStressTesting, gate: GateAuto, budget: AttemptBudget{n: 3}},
}

// fixerStep is the fixer's gate and budget. The fixer is on no path.
var fixerStep = PathStep{stage: StageFixer, gate: GateAuto, budget: AttemptBudget{n: 3}}

// FixerStep is the fixer's gate and attempt budget.
func FixerStep() PathStep { return fixerStep }

// PathFor returns the path new works of a work type start on.
func PathFor(t WorkType) (Path, error) {
	return PathOf(t, currentVersions[t])
}

// PathOf returns one version of a work type's path; the zero version is v1.
func PathOf(t WorkType, v PathVersion) (Path, error) {
	versions, ok := paths[t]
	if !ok {
		return Path{}, fmt.Errorf("path for work type %q: %w", t, ErrUnknownWorkType)
	}
	steps, ok := versions[v.orV1()]
	if !ok {
		return Path{}, fmt.Errorf("path %s for work type %q: %w", v, t, ErrUnknownPathVersion)
	}
	return Path{workType: t, version: v.orV1(), steps: steps}, nil
}

// Paths lists every version of every work type's path, by work type then
// version.
func Paths() []Path {
	var all []Path
	for _, t := range WorkTypes() {
		for n := 1; ; n++ {
			p, err := PathOf(t, PathVersion{n: n})
			if err != nil {
				break
			}
			all = append(all, p)
		}
	}
	return all
}

// WorkType is the work type the path is for.
func (p Path) WorkType() WorkType { return p.workType }

// Version is the path's version, never zero.
func (p Path) Version() PathVersion { return p.version }

func (p Path) isZero() bool { return len(p.steps) == 0 }

// Includes reports whether the stage is on the path.
func (p Path) Includes(stage Stage) bool {
	_, ok := p.indexOf(stage)
	return ok
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

// StressMandatory reports whether the path always enters stress testing: it
// is on the path and not optional. Review then makes no stress decision.
func (p Path) StressMandatory() bool {
	i, ok := p.indexOf(StageStressTesting)
	return ok && !p.steps[i].optional
}

// StressDecider is the stage whose agent decides whether stress testing
// runs: no work advances past it without a stress decision.
func StressDecider() Stage { return StageReview }

// stressDecisionCloses is the last stage during which the owner may still
// change the stress decision: after it, stress testing runs or the work is
// done.
var stressDecisionCloses = StageIntegrationTesting

// includes reports whether the work enters a step of its path: every step
// that isn't optional; stress testing once the work decided to include it;
// event modelling when the work started with an event model.
func (w active) includes(s PathStep) bool {
	if !s.optional {
		return true
	}
	switch s.stage {
	case StageStressTesting:
		return w.stress != nil && w.stress.choice.decision == StressInclude
	case StageEventModelling:
		return w.hasEventModel()
	}
	return false
}

// Skips reports whether the work leaves out an optional stage of its path:
// stress testing once it decided to skip it, event modelling when it
// started without an event model.
func (w active) Skips(stage Stage) bool {
	switch stage {
	case StageStressTesting:
		return w.stress != nil && w.stress.choice.decision == StressSkip
	case StageEventModelling:
		return w.path.Includes(stage) && !w.hasEventModel()
	}
	return false
}

// firstStep is the first step of the path the work enters.
func (w active) firstStep() PathStep {
	for _, s := range w.path.steps {
		if w.includes(s) {
			return s
		}
	}
	return w.path.steps[0]
}
