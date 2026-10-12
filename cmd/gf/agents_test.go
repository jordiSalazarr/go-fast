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

// instructions is where a stage's instructions live: its agent's file, or
// for grill, which runs in the main session with the owner, its skill.
func instructions(stage domain.Stage) string {
	if stage == domain.StageGrill {
		return filepath.Join("..", "..", "plugin", "skills", "grill", "SKILL.md")
	}
	return filepath.Join("..", "..", "plugin", "agents", stage.String()+".md")
}

// Each stage's instructions name the exit checks gf runs for its stage, as
// the domain lists them: those of every path the stage is on, in every
// version, then those only some paths add.
func TestStageAgents_NameTheirExitChecks(t *testing.T) {
	for _, stage := range domain.Stages() {
		body, err := os.ReadFile(instructions(stage))
		require.NoError(t, err)
		var byType [][]domain.ExitCheck
		for _, path := range domain.Paths() {
			if path.Includes(stage) || stage == domain.StageFixer {
				byType = append(byType, domain.RequiredChecks(stage, path))
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
		body, err := os.ReadFile(instructions(stage))
		require.NoError(t, err)
		if stage == domain.StageFixer {
			assert.Contains(t, string(body), "gf submit --passed --route <target>")
			continue
		}
		assert.Contains(t, string(body), "`current.reworkOf.fixerArtifact`", stage.String())
	}
}

// After grill, discovery maps the agreed behaviour onto the code and leaves
// conflicts with the plan to the owner.
func TestDiscoveryAgent_FitsTheAgreedBehaviourToTheCode(t *testing.T) {
	body, err := os.ReadFile(instructions(domain.StageDiscovery))
	require.NoError(t, err)
	for _, want := range []string{"**how the agreed behaviour fits the existing code**", "`work.grill.docPath`",
		"list each conflict under `## Decisions for the owner`", "## Expected behaviour` section (what it must do instead, as the event model and grill's decisions say)"} {
		assert.Contains(t, string(body), want)
	}
}

// The fixer may send the work back to event modelling, never to grill; the
// owner can.
func TestFixerAgent_RoutesToEventModelling_NotToGrill(t *testing.T) {
	body, err := os.ReadFile(instructions(domain.StageFixer))
	require.NoError(t, err)
	assert.Contains(t, string(body), "(→ `event-modelling`, only on work that started from an event model)")
	assert.Contains(t, string(body), "the owner may send the work back with `gf return grill \"<reason>\"`")
}

// The drive asks for feature work's inputs, and runs grill itself.
func TestDrive_AsksForTheInputs_AndRunsGrillInTheSession(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "skills", "drive", "SKILL.md"))
	require.NoError(t, err)
	for _, want := range []string{"adding `--spec <path>` and `--event-model <path>`", "load the `gofast:grill` skill",
		"tell the owner which decision document to read (`work.grill.docPath`)"} {
		assert.Contains(t, string(body), want)
	}
}

// Specify marks each spec with the scenarios it covers.
func TestSpecifyAgent_MarksTheScenarios(t *testing.T) {
	body, err := os.ReadFile(instructions(domain.StageSpecify))
	require.NoError(t, err)
	assert.Contains(t, string(body), "put a comment line `// gofast:scenario <ID>` directly above each test's `func`")
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
		paths = append(paths, instructions(stage))
	}
	for _, p := range paths {
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		for _, owner := range []string{"`gf approve`", "`gf reject`", "`gf extend`", "`gf rework-extend`", "`gf stress`", "`gf abandon`", "`gf return`", "`gf nfrs adopt`"} {
			assert.Contains(t, string(body), owner, "%s must say never to run %s", p, owner)
		}
	}
}
