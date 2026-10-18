package domain

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// SkillCatalog lists, per stage, the skills that stage may load only once
// the work has a diagnosis: the owner's specialised skills (memory,
// concurrency, postgres, …), which are wasted, or worse, before anyone
// knows what is slow. It is plugin content, owned by the owner.
type SkillCatalog struct {
	stages map[Stage][]string
}

// NewSkillCatalog builds a catalog from stage names to skill names, and
// lists what is wrong with it: unknown stages are left out, as are empty
// skill names.
func NewSkillCatalog(byStage map[string][]string) (SkillCatalog, []string) {
	c := SkillCatalog{stages: map[Stage][]string{}}
	var problems []string
	names := make([]string, 0, len(byStage))
	for name := range byStage {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		stage, err := ParseStage(name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("stage %q is not a gofast stage (stages: %s)", name, joinNames(Stages())))
			continue
		}
		for _, skill := range byStage[name] {
			skill = strings.TrimSpace(skill)
			if skill == "" {
				problems = append(problems, fmt.Sprintf("stage %s lists an empty skill name", stage))
				continue
			}
			c.stages[stage] = append(c.stages[stage], skill)
		}
	}
	return c, problems
}

// Skills are the catalog skills of a stage.
func (c SkillCatalog) Skills(stage Stage) []string { return slices.Clone(c.stages[stage]) }

// Stages are the stages with catalog skills, in the order of Stages().
func (c SkillCatalog) Stages() []Stage {
	var out []Stage
	for _, s := range Stages() {
		if len(c.stages[s]) > 0 {
			out = append(out, s)
		}
	}
	return out
}

// pluginPrefix is how Claude Code names a skill of the gofast plugin.
const pluginPrefix = "gofast:"

// CatalogSkill is the catalog name of a skill as the Skill tool names it:
// the plugin's own skills as "gofast:<name>" or plainly "<name>"; ok is
// false for another plugin's skill ("<plugin>:<name>").
func CatalogSkill(toolName string) (name string, ok bool) {
	name = strings.TrimSpace(toolName)
	if rest, found := strings.CutPrefix(name, pluginPrefix); found {
		return rest, rest != ""
	}
	return name, name != "" && !strings.Contains(name, ":")
}

// Diagnosis is what the unlock rule needs to know about the current stage
// visit of a work.
type Diagnosis struct {
	// Stage is the work's current stage.
	Stage Stage
	// Baseline: the work recorded a baseline (BaselineMeasured).
	Baseline bool
	// MeasureAccepted: the work's latest measure visit was accepted.
	MeasureAccepted bool
	// FixerMode is the fixer's mode, when Stage is the fixer.
	FixerMode FixerMode
	// Bottleneck: the fixer's current artifact has a non-empty
	// `## Bottleneck` section.
	Bottleneck bool
}

// MayLoad says whether the current stage may load a skill now: a skill in
// the stage's catalog is locked until a diagnosis exists. Optimize has one
// once measure recorded a baseline and was accepted; the fixer in
// performance mode once its artifact names the bottleneck. Every other
// stage, the fixer in correctness mode, and any skill not in the stage's
// catalog are not governed by this rule.
func (c SkillCatalog) MayLoad(skill string, d Diagnosis) error {
	if !slices.Contains(c.stages[d.Stage], skill) {
		return nil
	}
	switch {
	case d.Stage == StageOptimize && !(d.Baseline && d.MeasureAccepted):
		return &LockedSkillError{Skill: skill, Stage: d.Stage}
	case d.Stage == StageFixer && d.FixerMode == FixerModePerformance && !d.Bottleneck:
		return &LockedSkillError{Skill: skill, Stage: d.Stage}
	}
	return nil
}

// LockedSkillError names a catalog skill the stage may not load yet.
type LockedSkillError struct {
	Skill string
	Stage Stage
}

func (e *LockedSkillError) Error() string {
	return fmt.Sprintf("'%s' unlocks after the diagnosis: write `## Bottleneck` with evidence first.", e.Skill)
}

func (e *LockedSkillError) Unwrap() error { return ErrSkillLocked }

// SkillLoaded observes a skill loaded during a stage visit: how it was
// loaded, and the version loaded (zero when gf could not read it). It is
// no aggregate's event: it protects no invariant, and lives on the work's
// observations stream.
type SkillLoaded struct {
	WorkID  WorkID
	Stage   Stage
	Visit   Visit
	Skill   string
	Load    SkillLoad
	Version InstructionsVersion
}

func (SkillLoaded) isEvent() {}

// DiagnosisOf is what the unlock rule knows about a work's current stage
// visit: its baseline, whether measure was accepted (measureAccepted), and,
// in the fixer, its mode and whether its artifact names the bottleneck
// (bottleneck).
func DiagnosisOf(work ActiveWork, measureAccepted, bottleneck bool) Diagnosis {
	d := Diagnosis{Stage: work.CurrentStage(), Baseline: len(work.Baseline()) > 0, MeasureAccepted: measureAccepted}
	if fixer, ok := work.(InFixerWork); ok {
		d.FixerMode, d.Bottleneck = fixer.Mode(), bottleneck
	}
	return d
}
