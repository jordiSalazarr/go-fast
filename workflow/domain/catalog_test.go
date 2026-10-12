package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

func catalog(t *testing.T) domain.SkillCatalog {
	t.Helper()
	c, problems := domain.NewSkillCatalog(map[string][]string{
		"optimize": {"memory", "concurrency"}, "fixer": {"concurrency"}, "implement": {"postgres"},
	})
	require.Empty(t, problems)
	return c
}

func TestNewSkillCatalog_ReportsUnknownStages(t *testing.T) {
	c, problems := domain.NewSkillCatalog(map[string][]string{"optimise": {"memory"}, "optimize": {"memory", " "}})
	assert.Equal(t, []string{"stage optimize lists an empty skill name"}, problems[1:])
	assert.Contains(t, problems[0], `stage "optimise" is not a gofast stage`)
	assert.Equal(t, []string{"memory"}, c.Skills(domain.StageOptimize))
	assert.Equal(t, []domain.Stage{domain.StageOptimize}, c.Stages())
}

func TestCatalogSkill(t *testing.T) {
	for in, want := range map[string]string{"gofast:concurrency": "concurrency", "concurrency": "concurrency"} {
		got, ok := domain.CatalogSkill(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"other:concurrency", "", "gofast:"} {
		_, ok := domain.CatalogSkill(in)
		assert.False(t, ok, in)
	}
}

// A stage's catalog skill unlocks once a diagnosis exists; nothing else is
// governed by the rule.
func TestSkillCatalog_MayLoad(t *testing.T) {
	c := catalog(t)
	locked := func(err error) bool { return assert.ErrorIs(t, err, domain.ErrSkillLocked) }

	locked(c.MayLoad("concurrency", domain.Diagnosis{Stage: domain.StageOptimize}))
	locked(c.MayLoad("concurrency", domain.Diagnosis{Stage: domain.StageOptimize, Baseline: true}))
	locked(c.MayLoad("concurrency", domain.Diagnosis{Stage: domain.StageOptimize, MeasureAccepted: true}))
	assert.NoError(t, c.MayLoad("concurrency", domain.Diagnosis{Stage: domain.StageOptimize, Baseline: true, MeasureAccepted: true}))

	locked(c.MayLoad("concurrency", domain.Diagnosis{Stage: domain.StageFixer, FixerMode: domain.FixerModePerformance}))
	assert.NoError(t, c.MayLoad("concurrency", domain.Diagnosis{Stage: domain.StageFixer, FixerMode: domain.FixerModePerformance, Bottleneck: true}))
	assert.NoError(t, c.MayLoad("concurrency", domain.Diagnosis{Stage: domain.StageFixer, FixerMode: domain.FixerModeCorrectness}),
		"the rule governs the fixer in performance mode only")
	assert.NoError(t, c.MayLoad("memory", domain.Diagnosis{Stage: domain.StageFixer, FixerMode: domain.FixerModePerformance}),
		"not in the fixer's catalog")
	assert.NoError(t, c.MayLoad("postgres", domain.Diagnosis{Stage: domain.StageImplement}), "any other stage")
	assert.NoError(t, c.MayLoad("grill", domain.Diagnosis{Stage: domain.StageOptimize}), "not a catalog skill")

	err := c.MayLoad("concurrency", domain.Diagnosis{Stage: domain.StageOptimize})
	assert.EqualError(t, err, "'concurrency' unlocks after the diagnosis: write `## Bottleneck` with evidence first.")
}

func TestDiagnosisOf(t *testing.T) {
	history := onMeasure(t)
	w := asInProgress(t, givenWork(t, history...))
	events, err := w.RecordBaseline([]domain.NFRResult{latency.Evaluate(slow)})
	require.NoError(t, err)
	optimize := asInProgress(t, givenWork(t, advance(t, append(history, events...))...))
	assert.Equal(t, domain.Diagnosis{Stage: domain.StageOptimize, Baseline: true, MeasureAccepted: true}, domain.DiagnosisOf(optimize, true, false))
	assert.Equal(t, domain.Diagnosis{Stage: domain.StageMeasure}, domain.DiagnosisOf(w, false, true), "bottleneck counts in the fixer only")
}
