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
	"github.com/jordiSalazarr/go-fast/workflow/skillcatalog"
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

// On v3 feature work, discovery maps the owner's input onto the code before
// grill and leaves its questions to grill; on v2, after grill, it maps the
// agreed behaviour and leaves conflicts with the plan to the owner.
func TestDiscoveryAgent_FitsTheInputOrTheAgreedBehaviourToTheCode(t *testing.T) {
	body, err := os.ReadFile(instructions(domain.StageDiscovery))
	require.NoError(t, err)
	for _, want := range []string{"(`work.pathVersion` is `v3`), discovery runs **before grill**", "**where the owner's input fits the existing code**",
		"end the artifact with `## Questions for grill`", "It has no `## Decisions for the owner`.",
		"on v3, as the input asks for it, which grill then settles", "on v3 a pass moves the work to grill, on v2 to design",
		"**how the agreed behaviour fits the existing code**", "`work.grill.docPath`",
		"list each conflict under `## Decisions for the owner`", "on v2, as the event model and grill's decisions say), neither empty"} {
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
		"tell the owner which decision document to read (`work.grill.docPath`)",
		"New feature work goes: event modelling (only with an event model), discovery, grill (here, live with the owner), design",
		"grill runs after discovery, starting from discovery's `## Questions for grill`"} {
		assert.Contains(t, string(body), want)
	}
}

// Specify marks each spec with the scenarios it covers.
func TestSpecifyAgent_MarksTheScenarios(t *testing.T) {
	body, err := os.ReadFile(instructions(domain.StageSpecify))
	require.NoError(t, err)
	assert.Contains(t, string(body), "put a comment line `// gofast:scenario <ID>` directly above each test's `func`")
	assert.Contains(t, string(body), "**every marked spec must fail before implement, and fail for the right reason**")
}

// Review decides stress testing as it passes.
func TestReviewAgent_DecidesStressTesting(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "agents", "review.md"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "Decide stress testing: `--stress include` if the change can affect behaviour under load (concurrency, I/O, queries, hot paths), else `--stress skip` with a reason. If gf says it's forced")
	assert.Contains(t, string(body), "`gf submit --passed --stress <include|skip> \"<reason>\"`")
	assert.Contains(t, string(body), "If your verdict is Failed, run `gf submit --failed \"<n> blocking: B1 …\"`; that hands the work to the fixer. "+
		"Submit `--passed` only with a Passed verdict and no blocking findings; `review-verdict-consistent` checks it.")
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

// The plugin ships its skill catalogs empty: the owner's skills slot in
// later. Every stage it names is a stage, and every skill has a directory.
func TestPlugin_SkillCatalogIsValid(t *testing.T) {
	catalog, problems, err := skillcatalog.Load(filepath.Join("..", "..", "plugin"))
	require.NoError(t, err)
	assert.Empty(t, problems)
	assert.Empty(t, catalog.Stages(), "the catalogs ship empty")
	body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "skills", "catalog.json"))
	require.NoError(t, err)
	assert.Contains(t, string(body), `"optimize": []`)
	assert.Contains(t, string(body), `"fixer": []`)
}

// The refactor's agents: plan follows grill's decisions and never codes;
// characterize pins behaviour with passing tests; implement stays in the
// plan's areas and keeps every test.
func TestRefactorAgents(t *testing.T) {
	read := func(stage domain.Stage) string {
		body, err := os.ReadFile(instructions(stage))
		require.NoError(t, err)
		return string(body)
	}
	plan := read(domain.StagePlan)
	for _, want := range []string{"**links the decision document**", "`refactor-plan-v<visit>.json`", "`behaviourChange` is always `false`",
		"`## Decisions for the owner`", "You plan; you never change code or tests."} {
		assert.Contains(t, plan, want)
	}
	characterize := read(domain.StageCharacterize)
	for _, want := range []string{"tests that **pass** against the current code", "`work.grill.mustNotChange`", "each item you judge not testable in Go, with the reason",
		"you never change production code"} {
		assert.Contains(t, characterize, want)
	}
	implement := read(domain.StageImplement)
	assert.Contains(t, implement, "Change only the plan's `areas`, adding new files only under the directories they name; never modify or delete an existing test")
	assert.Contains(t, read(domain.StageDiscovery), "map **the code about to move**")
}

// The performance agents: measure records a baseline and diagnoses with
// evidence; optimize may load catalog skills and never edits existing
// tests; the fixer writes `## Bottleneck` first, which unlocks its catalog.
func TestPerformanceAgents(t *testing.T) {
	read := func(stage domain.Stage) string {
		body, err := os.ReadFile(instructions(stage))
		require.NoError(t, err)
		return string(body)
	}
	measure := read(domain.StageMeasure)
	for _, want := range []string{"`<work.artifactDir>stress/<nfr-id>.js`", "a `## Bottleneck` section", "you never change code or tests", "there is nothing to improve and the work completes"} {
		assert.Contains(t, measure, want)
	}
	optimize := read(domain.StageOptimize)
	assert.Contains(t, optimize, "the performance skills in this stage's catalog are unlocked")
	assert.Contains(t, optimize, "never modify or delete an existing test")
	assert.Contains(t, optimize, "tools: Read, Grep, Glob, Bash, Write, Edit, Skill")
	fixer := read(domain.StageFixer)
	assert.Contains(t, fixer, "as a `## Bottleneck` section in your artifact: until it is there, the performance skills of the fixer's catalog stay locked")
	assert.Contains(t, fixer, "(→ `measure`)")
	assert.Contains(t, fixer, "(→ `plan`)")
}

// The drive offers all five work types, asks for the target NFRs, and
// points the owner to the plan's decisions.
func TestDrive_OffersEveryWorkType(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "plugin", "skills", "drive", "SKILL.md"))
	require.NoError(t, err)
	for _, typ := range domain.WorkTypes() {
		assert.Contains(t, string(body), "`"+typ.String()+"`")
	}
	for _, want := range []string{"`--nfr <id>` per target NFR", "When plan waits for approval", "`## Decisions for the owner`", "found nothing to improve"} {
		assert.Contains(t, string(body), want)
	}
}

// frontmatter returns a plugin file's YAML frontmatter lines.
func frontmatter(t *testing.T, path string) []string {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	parts := strings.SplitN(string(body), "---\n", 3)
	require.Len(t, parts, 3, "%s has frontmatter", path)
	return strings.Split(parts[1], "\n")
}

// preloads are the skills an agent's frontmatter preloads, as written.
func preloads(t *testing.T, stage domain.Stage) []string {
	t.Helper()
	var skills []string
	in := false
	for _, line := range frontmatter(t, instructions(stage)) {
		switch {
		case line == "skills:":
			in = true
		case in && strings.HasPrefix(line, "  - "):
			skills = append(skills, strings.TrimPrefix(line, "  - "))
		default:
			in = false
		}
	}
	return skills
}

// Each stage agent preloads the owner's skills for its stage, by their full
// name: a bare name can resolve to a same-named skill outside the plugin.
// Every preload is a skill of the plugin that Claude may load (preloading
// can't load one with disable-model-invocation), and none is a catalog
// skill: preloaded skills are core, never locked.
func TestStageAgents_PreloadTheOwnersSkills(t *testing.T) {
	want := map[domain.Stage][]string{
		domain.StageDiscovery:    {"discovery", "code-layout"},
		domain.StageDesign:       {"aggregate-design", "functional-domain", "value-types", "schema-evolution", "reads-and-reactions"},
		domain.StagePlan:         {"code-layout"},
		domain.StageCharacterize: {"specify"},
		domain.StageSpecify:      {"specify", "functional-domain"},
		domain.StageImplement:    {"aggregate-design", "functional-domain", "value-types", "code-layout", "schema-evolution", "reads-and-reactions"},
		domain.StageOptimize:     {"functional-domain", "code-layout"},
		domain.StageReview:       {"review", "aggregate-design", "functional-domain", "value-types", "code-layout", "schema-evolution", "reads-and-reactions"},
		domain.StageFixer:        {"fixer", "discovery"},
	}
	catalog, _, err := skillcatalog.Load(filepath.Join("..", "..", "plugin"))
	require.NoError(t, err)
	for _, stage := range domain.Stages() {
		if stage == domain.StageGrill {
			continue // grill runs in the main session; its skill loads grilling and domain-modeling
		}
		var full []string
		for _, s := range want[stage] {
			full = append(full, "gofast:"+s)
		}
		assert.Equal(t, full, preloads(t, stage), stage.String())
		body, err := os.ReadFile(instructions(stage))
		require.NoError(t, err)
		rule := "Do the work the way your preloaded skills describe. Where a skill and this protocol differ on gf commands, write scopes, checks or artifact paths, the protocol wins.\n\n"
		if len(full) > 0 {
			assert.Contains(t, string(body), rule, stage.String())
		} else {
			assert.NotContains(t, string(body), "preloaded skills", stage.String())
		}
		for _, name := range want[stage] {
			skill := frontmatter(t, filepath.Join("..", "..", "plugin", "skills", name, "SKILL.md"))
			assert.Contains(t, skill, "name: "+name, "%s preloads %s", stage, name)
			assert.NotContains(t, skill, "disable-model-invocation: true", "%s preloads %s", stage, name)
			for _, s := range domain.Stages() {
				assert.NotContains(t, catalog.Skills(s), name, "%s is preloaded, so never a catalog skill", name)
			}
		}
	}
	assert.Contains(t, string(must(os.ReadFile(instructions(domain.StageCharacterize)))),
		"From the specify skill, use the shape of a spec (one behaviour per test, given/when/then, observable outcomes, deterministic). Characterization tests pass against the current code; they pin behaviour, they don't specify new behaviour.")
}

// Grill loads the owner's interview method itself: the vendored grilling and
// domain-modeling skills, kept with their licence and their source.
func TestGrill_LoadsTheVendoredInterviewSkills(t *testing.T) {
	body, err := os.ReadFile(instructions(domain.StageGrill))
	require.NoError(t, err)
	assert.Contains(t, string(body), "Load the `gofast:grilling` and `gofast:domain-modeling` skills with the Skill tool")
	for _, name := range []string{"grilling", "domain-modeling"} {
		dir := filepath.Join("..", "..", "plugin", "skills", name)
		assert.Contains(t, frontmatter(t, filepath.Join(dir, "SKILL.md")), "name: "+name)
		assert.FileExists(t, filepath.Join(dir, "LICENSE"))
		source, err := os.ReadFile(filepath.Join(dir, "SOURCE.md"))
		require.NoError(t, err)
		assert.Contains(t, string(source), "49dd158")
	}
}

// Discovery and the fixer have no tools field, so they inherit the
// session's MCP tools: the repository's observability servers, which they
// use read-only, never copying secrets into committed artifacts.
func TestDiscoveryAndFixer_InheritTheSessionsObservabilityTools(t *testing.T) {
	for _, stage := range []domain.Stage{domain.StageDiscovery, domain.StageFixer} {
		for _, line := range frontmatter(t, instructions(stage)) {
			assert.False(t, strings.HasPrefix(line, "tools:"), "%s must not restrict its tools", stage)
		}
		body := string(must(os.ReadFile(instructions(stage))))
		assert.Contains(t, body, "use any connected observability tools (logs, traces, metrics, alerts) **read-only**", stage.String())
		assert.Contains(t, body, "never copy secrets or personal data into your artifact", stage.String())
	}
}
