package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// Each stage agent names the exit checks gf runs for its stage, as the
// domain lists them: those of every path the stage is on, then those only
// some paths add.
func TestStageAgents_NameTheirExitChecks(t *testing.T) {
	for _, stage := range domain.Stages() {
		body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", stage.String()+".md"))
		require.NoError(t, err)
		var byType [][]domain.ExitCheck
		for _, workType := range domain.WorkTypes() {
			if path, _ := domain.PathFor(workType); path.Includes(stage) || stage == domain.StageFixer {
				byType = append(byType, domain.RequiredChecks(stage, workType))
			}
		}
		var common []string
		for _, c := range byType[0] {
			if !slices.ContainsFunc(byType, func(checks []domain.ExitCheck) bool { return !slices.Contains(checks, c) }) {
				common = append(common, "`"+c.String()+"`")
			}
		}
		assert.Contains(t, string(body), "`gf submit --passed` runs these checks itself: "+strings.Join(common, ", ")+".", stage.String())
		for _, checks := range byType {
			for _, c := range checks {
				assert.Contains(t, string(body), "`"+c.String()+"`", stage.String())
			}
		}
	}
}

// Every stage agent but the fixer reads the fixer's artifact first on a
// visit after a rework; the fixer chooses its route.
func TestStageAgents_KnowAboutRework(t *testing.T) {
	for _, stage := range domain.Stages() {
		body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", stage.String()+".md"))
		require.NoError(t, err)
		if stage == domain.StageFixer {
			assert.Contains(t, string(body), "gf submit --passed --route <target>")
			continue
		}
		assert.Contains(t, string(body), "`current.reworkOf.fixerArtifact`", stage.String())
	}
}

// Review decides stress testing as it passes.
func TestReviewAgent_DecidesStressTesting(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", "review.md"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "Decide stress testing: `--stress include` if the change can affect behaviour under load (concurrency, I/O, queries, hot paths), else `--stress skip` with a reason. If gf says it's forced")
	assert.Contains(t, string(body), "`gf submit --passed --stress <include|skip> \"<reason>\"`")
}

// The stress-testing agent writes one script per NFR, at the path gf reads,
// and never sets what gf measures.
func TestStressTestingAgent_WritesScriptsGfRuns(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", "stress-testing.md"))
	require.NoError(t, err)
	for _, want := range []string{"`<work.artifactDir>stress/<nfr-id>.js`", "`__ENV.BASE_URL`", "`current.stress.mandatory`",
		"Never** define thresholds, a summary or load options in a script; never change code or tests"} {
		assert.Contains(t, string(body), want)
	}
}

// In performance mode, the fixer names the bottleneck with evidence first.
func TestFixerAgent_DiagnosesFirstInPerformanceMode(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", "fixer.md"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "In **performance** mode (`current.fixer.mode`, after stress testing missed an NFR), diagnose first: name the bottleneck with evidence")
	assert.Contains(t, string(body), "`current.fixer.stressRunDir`")
}

// No agent, and not the drive, runs an owner-only command.
func TestAgentsAndDrive_NeverRunOwnerCommands(t *testing.T) {
	paths := []string{filepath.Join("..", "..", "plugin", "skills", "drive", "SKILL.md")}
	for _, stage := range domain.Stages() {
		paths = append(paths, filepath.Join("..", "..", "plugin", "agents", stage.String()+".md"))
	}
	for _, p := range paths {
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		for _, owner := range []string{"`gf approve`", "`gf reject`", "`gf extend`", "`gf rework-extend`", "`gf stress`", "`gf abandon`"} {
			assert.Contains(t, string(body), owner, "%s must say never to run %s", p, owner)
		}
	}
}
