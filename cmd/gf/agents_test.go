package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// Each stage agent names the exit checks gf runs for its stage, as the
// domain lists them.
func TestStageAgents_NameTheirExitChecks(t *testing.T) {
	for _, stage := range domain.Stages() {
		body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", stage.String()+".md"))
		require.NoError(t, err)
		names := make([]string, 0, len(stage.RequiredChecks()))
		for _, c := range stage.RequiredChecks() {
			names = append(names, "`"+c.String()+"`")
		}
		assert.Contains(t, string(body), "`gf submit --passed` runs these checks itself: "+strings.Join(names, ", ")+".", stage.String())
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
