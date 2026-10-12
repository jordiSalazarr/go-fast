package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/progress"
)

// The refactor and improve-performance paths end to end, in a temporary Go
// module; performance with the fake k6.

const (
	// refactoredServer keeps Port's behaviour, built differently.
	refactoredServer = "package server\n\n// defaultPort is the port the server listens on.\nconst defaultPort = 8081\n\n// Port is the port the server listens on.\nfunc Port() int { return defaultPort }\n"
	// characterization pins what Port does now.
	characterization = "package server\n\nimport \"testing\"\n\nfunc TestPort_Is8081(t *testing.T) {\n\tif got := Port(); got != 8081 {\n\t\tt.Fatalf(\"Port() = %d, want 8081\", got)\n\t}\n}\n"
)

// refactorDocument is a refactor's decision document; mustNotChange is its
// `## Must not change` section, or none.
func refactorDocument(mustNotChange string) string {
	doc := "# Split the server\n\n## Intent\n\nName the default port.\n\n## Scope\n\nIn: server/. Out: everything else.\n\n" +
		"## Decisions\n\n- D1: Keep Port's signature? Yes: callers depend on it.\n\n## Glossary\n\nNone.\n\n"
	if mustNotChange != "" {
		doc += "## Must not change\n\n" + mustNotChange + "\n\n"
	}
	return doc + "## Open questions\n\nNone.\n"
}

const refactorD1 = `{"id": "D1", "question": "Keep Port's signature?", "answer": "Yes.", "rationale": "Callers depend on it."}`

// refactorGrillWrites writes a refactor's grill outputs.
func (r *repo) refactorGrillWrites(mustNotChangeDoc, mustNotChangeJSON string) string {
	r.t.Helper()
	w := r.status().Work
	doc := w.DecisionsDir + "/2026-10-12-" + w.ID + "-split-the-server.md"
	r.write(doc, refactorDocument(mustNotChangeDoc))
	r.write(strings.TrimSuffix(r.status().Current.Artifact, ".md")+".json",
		fmt.Sprintf(`{"decisions": [%s], "mustNotChange": [%s], "openQuestions": [], "docPath": %q}`, refactorD1, mustNotChangeJSON, doc))
	r.writeArtifact("The intent is in " + doc + ".")
	return doc
}

// newRefactorRepo is newRepo's module, with the owner's checks: an
// integration command and a minimum coverage of 80%.
func newRefactorRepo(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	r.write(".gofast/checks.json", `{"integration": {"command": ["go", "test", "./..."]}, "timeout": "2m", "refactor": {"minCoverage": 80}}`)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "initial")
	return r
}

// toRefactorPlan starts a refactor and takes it through discovery and an
// approved grill, to plan's first visit; it returns the decision document.
func (r *repo) toRefactorPlan() string {
	r.t.Helper()
	r.gf("start", "--type", "refactor", "Split the server")
	r.workOnStage()
	r.submitAsAgent("--passed") // discovery's gate is auto
	r.expect("grill", progress.AssignmentOpen, 0, 3)
	doc := r.refactorGrillWrites("- `Port()` returns 8081", `"Port() returns 8081"`)
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.expect("plan", progress.AssignmentOpen, 0, 3)
	return doc
}

func (r *repo) writePlan(markdown string) {
	r.t.Helper()
	v := r.status()
	r.write(v.Current.Artifact, markdown)
	r.write(fmt.Sprintf("%srefactor-plan-v%d.json", v.Work.ArtifactDir, v.Current.Visit), `{"areas": ["server/**"], "moves": [], "behaviourChange": false}`)
}

func TestRefactorPath_EndToEnd(t *testing.T) {
	r := newRefactorRepo(t)
	r.gf("start", "--type", "refactor", "Split the server")
	v := r.expect("discovery", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"current", "pending", "pending", "pending", "pending", "pending", "pending", "pending"}, stageStatuses(v))
	r.workOnStage()
	r.submitAsAgent("--passed")

	// Grill: without `## Must not change` it fails; with it, it passes and
	// the owner approves the intent.
	r.expect("grill", progress.AssignmentOpen, 0, 3)
	r.refactorGrillWrites("", "")
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "a refactor's grill needs mustNotChange")
	log := r.readFile(".gofast/runtime/checks/" + r.status().Work.ID + "-grill-1-attempt1.log")
	assert.Contains(t, log, "no '## Must not change' section")
	doc := r.refactorGrillWrites("- `Port()` returns 8081", `"Port() returns 8081"`)
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "'grill' passed its exit checks and waits for the owner")
	v = r.status()
	assert.Equal(t, []string{"Port() returns 8081"}, v.Work.Grill.MustNotChange)
	assert.Contains(t, v.Next.Message, "its `## Must not change` above all")
	r.gf("approve")

	// Plan: without the grill link it fails; the owner rejects the next
	// attempt with feedback, then approves.
	v = r.expect("plan", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "plan-valid"}, v.Current.Checks)
	r.writePlan("# Plan\n\nName the port constant.\n")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "doesn't link the accepted grill's decision document, "+doc)
	r.writePlan("# Plan\n\nFollows " + doc + ": name the port constant.\n")
	r.submitAsAgent("--passed")
	r.expect("plan", progress.AssignmentAwaitingApproval, 1, 3)
	r.gf("reject", "say which file the constant goes in")
	r.writePlan("# Plan\n\nFollows " + doc + ": name the port constant in server/server.go.\n")
	r.submitAsAgent("--passed")
	v = r.expect("plan", progress.AssignmentAwaitingApproval, 2, 3)
	assert.Equal(t, []string{"server/**"}, v.Work.Plan.Areas)
	r.gf("approve")

	// Characterize: below the owner's 80% it fails; a passing test pins Port.
	v = r.expect("characterize", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "characterization-passes", "coverage-sufficient"}, v.Current.Checks)
	r.workOnStage()
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "below the owner's minimum coverage of 80%: server 0%")
	r.write("server/port_test.go", characterization)
	r.workOnStage()
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ coverage-sufficient: server 100% (1 of 1 statements)")

	// Implement: editing a characterization test fails tests-untouched;
	// writing outside the plan's areas fails the scope; the refactor passes.
	v = r.expect("implement", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"artifact-written", "build-passes", "formatted", "tests-untouched", "suite-passes"}, v.Current.Checks)
	assert.Equal(t, 100.0, v.Work.Coverage.Packages[0].Percent)
	r.write("server/server.go", refactoredServer)
	r.write("server/port_test.go", strings.Replace(characterization, "8081", "8082", -1))
	r.write("server/server.go", strings.Replace(refactoredServer, "8081", "8082", 1))
	r.writeArtifact("Named the port constant.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "tests-untouched: existing tests were changed: server/port_test.go (modified)")
	r.write("server/port_test.go", characterization)
	r.write("server/server.go", refactoredServer)
	r.write("cmd/tool/main.go", "package main\n\nfunc main() {}\n")
	r.writeArtifact("Named the port constant.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "Changed files outside the 'implement' write scope, the plan's areas (server/**) and new files under their directories: cmd/tool/main.go.")
	r.remove("cmd/tool/main.go")
	r.writeArtifact("Named the port constant.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "'implement' accepted.")

	// Review, integration testing, completed.
	r.workOnStage()
	r.submitAsAgent(reviewPass...)
	r.workOnStage()
	r.submitAsAgent("--passed")
	assert.Nil(t, r.status().Work, "work completed")

	index := r.readFile("docs/decisions/README.md")
	assert.Contains(t, index, "Split the server")
	assert.Contains(t, index, "refactor")
}

// The owner sends a refactor back from characterize to plan: plan enters
// its second visit, and no rework is used.
func TestRefactor_ReturnFromCharacterizeToPlan(t *testing.T) {
	r := newRefactorRepo(t)
	doc := r.toRefactorPlan()
	r.writePlan("# Plan\n\nFollows " + doc + ".\n")
	r.submitAsAgent("--passed")
	r.gf("approve")
	r.expect("characterize", progress.AssignmentOpen, 0, 3)

	out := r.gf("return", "plan", "the plan misses the config package")
	assert.Contains(t, out, "Returned refactor work \"Split the server\" from 'characterize' to 'plan'.")
	v := r.expect("plan", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, 2, v.Current.Visit)
	assert.Equal(t, 0, v.Work.Reworks)
	require.NotNil(t, v.Current.Returned)
	assert.Equal(t, "the plan misses the config package", v.Current.Returned.Reason)
}

// walletTest is an existing test of the wallet server.
const walletTest = "package server\n\nimport (\n\t\"net/http\"\n\t\"net/http/httptest\"\n\t\"testing\"\n)\n\n" +
	"func TestWallet_PostsATransaction(t *testing.T) {\n\trec := httptest.NewRecorder()\n\tHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, \"/wallets/w1/transactions\", nil))\n" +
	"\tif rec.Code != http.StatusCreated {\n\t\tt.Fatalf(\"status %d\", rec.Code)\n\t}\n}\n"

// newPerformanceRepo is the stress repository, its wallet as slow as
// delay, with an existing test of the wallet.
func newPerformanceRepo(t *testing.T, delay string) *repo {
	t.Helper()
	withFakeK6(t)
	r := newStressRepo(t, `"server/**"`, delay)
	r.write("server/wallet_test.go", walletTest)
	r.git("add", "-A")
	r.git("commit", "-q", "-m", "wallet test")
	return r
}

// measureWrites writes measure's script, profile and diagnosis.
func (r *repo) measureWrites() {
	r.t.Helper()
	r.writeScript(walletScript)
	dir := r.status().Work.ArtifactDir
	r.write(dir+"profiles/cpu.pprof", "profile")
	r.write(r.status().Current.Artifact, "# Measure\n\n## Bottleneck\n\nThe handler sleeps per transaction: profiles/cpu.pprof.\n")
}

// toMeasure starts improve-performance work targeting wallet-latency, and
// passes discovery.
func (r *repo) toMeasure() progress.View {
	r.t.Helper()
	out := r.gf("start", "--type", "improve-performance", "--nfr", "wallet-latency", "Post transactions faster")
	assert.Contains(r.t, out, "Target NFR: wallet-latency.")
	r.workOnStage()
	r.submitAsAgent("--passed")
	return r.expect("measure", progress.AssignmentOpen, 0, 3)
}

// skillCatalog is a plugin with a catalog that locks concurrency on
// optimize.
func skillCatalog(t *testing.T) map[string]string {
	t.Helper()
	plugin := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(plugin, "skills", "concurrency"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(plugin, "skills", "catalog.json"), []byte(`{"stages": {"optimize": ["concurrency"]}}`), 0o644))
	return map[string]string{"CLAUDE_PLUGIN_ROOT": plugin}
}

func loadSkill(agentType, skill string) map[string]any {
	return preToolUse(agentType, "Skill", map[string]any{"skill": skill, "args": ""})
}

func TestImprovePerformancePath_EndToEnd(t *testing.T) {
	r := newPerformanceRepo(t, slowWallet)

	res := r.gfFails(nil, "start", "--type", "improve-performance", "Post transactions faster")
	assert.Contains(t, res.stderr, "Improve-performance work targets the NFRs it must meet: `gf start --type improve-performance --nfr <id>")
	res = r.gfFails(nil, "start", "--type", "improve-performance", "--nfr", "checkout-latency", "Post transactions faster")
	assert.Equal(t, "The NFR 'checkout-latency' is not in .gofast/nfrs.json. Its NFRs: wallet-latency.\n", res.stderr)
	res = r.gfFails(nil, "start", "--type", "fix-bug", "--nfr", "wallet-latency", "x")
	assert.Equal(t, "Target NFRs (--nfr) are for improve-performance work.\n", res.stderr)

	v := r.toMeasure()
	assert.Equal(t, []string{"wallet-latency"}, v.Work.TargetNFRs)
	assert.Equal(t, []string{"artifact-written", "stress-scripts-valid", "baseline-recorded", "diagnosis-written"}, v.Current.Checks)

	// Catalog skills are locked until measure is accepted.
	plugin := skillCatalog(t)
	r.measureWrites()
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ baseline-recorded: wallet-latency p95 ")
	assert.Contains(t, out, "Baseline of wallet-latency: p95 ")
	assert.Contains(t, r.eventTypes(), "BaselineMeasured")

	// Optimize: the diagnosis exists, so the catalog skill loads, and the
	// load is recorded.
	v = r.expect("optimize", progress.AssignmentOpen, 0, 3)
	require.Len(t, v.Work.Baseline, 1)
	assert.False(t, v.Work.Baseline[0].Met, "the slow wallet misses the target")
	assert.Empty(t, r.hook(plugin, "pre-tool-use", loadSkill("gofast:optimize", "gofast:concurrency")))
	assert.Empty(t, r.hook(plugin, "post-tool-use", map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Skill",
		"tool_input": map[string]any{"skill": "gofast:concurrency"}, "tool_response": map[string]any{"success": true}}))
	v = r.status()
	assert.Equal(t, []string{"concurrency"}, v.Current.SkillsLoaded)
	assert.Contains(t, r.eventTypes(), "SkillLoaded")

	// Editing an existing test fails tests-untouched; the fix passes.
	r.write("server/wallet_test.go", strings.Replace(walletTest, "StatusCreated", "StatusCreated && false", 1))
	r.write("server/wallet.go", fmt.Sprintf(walletServer, fastWallet))
	r.workOnStage()
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "tests-untouched: existing tests were changed: server/wallet_test.go (modified)")
	r.write("server/wallet_test.go", walletTest)
	r.workOnStage()
	r.submitAsAgent("--passed")

	// Review makes no stress decision.
	v = r.expect("review", progress.AssignmentOpen, 0, 3)
	assert.Equal(t, []string{"gf submit --passed", `gf submit --failed "<reason>"`}, v.Next.Commands)
	r.workOnStage()
	res = r.runWithoutTerminal(asAgent, "submit", "--passed", "--stress", "skip", skipStress)
	require.Equal(t, 1, res.code)
	assert.Contains(t, res.stderr, "Improve-performance work always runs stress testing: review makes no stress decision.")
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "'review' accepted, deciding to include stress testing (mandatory: wallet-latency).")
	r.workOnStage()
	r.submitAsAgent("--passed")

	// Stress testing meets the target, and shows the change against the
	// baseline; the work completes.
	v = r.expect("stress-testing", progress.AssignmentOpen, 0, 3)
	require.Len(t, v.Current.Stress.Mandatory, 1)
	r.workOnStage()
	out = r.submitAsAgent("--passed")
	assert.Contains(t, out, "✓ nfrs-met: against the baseline: wallet-latency: p95 ")
	assert.Contains(t, out, "(target 40ms)")
	assert.Nil(t, r.status().Work, "work completed")
}

// A catalog skill is denied while optimize has no diagnosis: here the fixer
// in performance mode, before its artifact names the bottleneck.
func TestCatalogSkill_IsDeniedUntilTheDiagnosis(t *testing.T) {
	r := newPerformanceRepo(t, slowWallet)
	r.toMeasure()
	plugin := skillCatalog(t)
	// On measure, concurrency is in no catalog of the stage: allowed.
	assert.Empty(t, r.hook(plugin, "pre-tool-use", loadSkill("gofast:measure", "gofast:concurrency")))

	catalog := filepath.Join(plugin["CLAUDE_PLUGIN_ROOT"], "skills", "catalog.json")
	require.NoError(t, os.WriteFile(catalog, []byte(`{"stages": {"measure": ["concurrency"]}}`), 0o644))
	assert.Empty(t, r.hook(plugin, "pre-tool-use", loadSkill("gofast:measure", "gofast:concurrency")), "measure isn't governed by the rule")

	require.NoError(t, os.WriteFile(catalog, []byte(`{"stages": {"optimize": ["concurrency"]}}`), 0o644))
	r.measureWrites()
	r.submitAsAgent("--passed")
	r.expect("optimize", progress.AssignmentOpen, 0, 3)
	assert.Empty(t, r.hook(plugin, "pre-tool-use", loadSkill("gofast:optimize", "gofast:concurrency")), "unlocked by measure's diagnosis")
}

// Every target already meets its NFR at baseline: the work completes with
// nothing to improve.
func TestImprovePerformance_NothingToImprove(t *testing.T) {
	r := newPerformanceRepo(t, fastWallet)
	r.toMeasure()
	r.measureWrites()
	out := r.submitAsAgent("--passed")
	assert.Contains(t, out, "Every target NFR already meets its targets at baseline: there is nothing to improve, so the work is complete.")
	assert.Contains(t, r.eventTypes(), "WorkFoundNothingToImprove")
	v := r.status()
	assert.Nil(t, v.Work)
	assert.Contains(t, v.Next.Message, "found nothing to improve")
}
