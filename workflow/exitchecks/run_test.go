package exitchecks_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/eventlog"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

// module is a temporary git repository holding a Go module with a bug:
// Port returns 8081 instead of 8080.
type module struct {
	t    *testing.T
	root string
	// eventModel: the work started from an event model, whose first
	// event-modelling visit was accepted before grill's first visit.
	eventModel bool
	// grill: the work's first grill visit was accepted, as specify's
	// baseline has it.
	grill bool
}

func newModule(t *testing.T) *module {
	t.Helper()
	m := &module{t: t, root: t.TempDir()}
	m.git("init", "-q", "-b", "main")
	m.write("go.mod", "module example.com/server\n\ngo 1.24\n")
	m.write("server/server.go", "package server\n\nfunc Port() int { return 8081 }\n")
	m.write("server/config_test.go", "package server\n\nimport \"testing\"\n\nfunc TestConfig(t *testing.T) {}\n")
	m.git("add", "-A")
	m.git("-c", "user.name=T", "-c", "user.email=t@x", "commit", "-q", "-m", "initial")
	require.NoError(t, eventlog.Init(m.root))
	return m
}

func (m *module) git(args ...string) {
	m.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", m.root}, args...)...).CombinedOutput()
	require.NoError(m.t, err, string(out))
}

func (m *module) write(name, content string) {
	m.t.Helper()
	path := filepath.Join(m.root, filepath.FromSlash(name))
	require.NoError(m.t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(m.t, os.WriteFile(path, []byte(content), 0o644))
}

func (m *module) remove(name string) {
	m.t.Helper()
	require.NoError(m.t, os.Remove(filepath.Join(m.root, filepath.FromSlash(name))))
}

// begin records the baseline of a stage visit, as opening its assignment does.
func (m *module) begin(stage domain.Stage) {
	m.t.Helper()
	_, err := worktree.OpenBaselines(m.root).Ensure(AssignmentOn(stage))
	require.NoError(m.t, err)
}

// writeArtifact writes the stage visit's artifact.
func (m *module) writeArtifact(stage domain.Stage) {
	m.t.Helper()
	m.write(domain.ArtifactFor(WorkID, stage, Visit1).String(), "# "+stage.String()+"\n\nDone.\n")
}

// check runs the stage's exit checks on fix-bug's path and returns their
// results by check.
func (m *module) check(stage domain.Stage, config exitchecks.Config) map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	return m.checkOn(Must(domain.PathFor(domain.WorkTypeFixBug)), stage, config)
}

// checkOn runs the stage's exit checks on a work type's path and returns
// their results by check.
func (m *module) checkOn(path domain.Path, stage domain.Stage, config exitchecks.Config) map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	changed, started, err := worktree.OpenBaselines(m.root).ChangedSince(AssignmentOn(stage))
	require.NoError(m.t, err)
	require.False(m.t, started, "begin the stage first")
	if config.Timeout == 0 {
		config.Timeout = time.Minute
	}
	if config.Decisions == (domain.DecisionsDir{}) {
		config.Decisions = domain.DefaultDecisionsDir()
	}
	runner := exitchecks.Runner{Root: m.root, Config: config, Env: append(os.Environ(), "GF_ACTOR=agent")}
	visit := exitchecks.Visit{
		Assignment: AssignmentOn(stage), Work: WorkID, Path: path, Stage: stage, Visit: Visit1, Attempt: Attempt(1),
		Specify: AssignmentOn(domain.StageSpecify), Changed: changed,
	}
	if path.Includes(domain.StageDesign) {
		// The design's first visit was accepted before specify's first.
		visit.Design, visit.DesignEnd = Visit1, AssignmentOn(domain.StageSpecify)
	}
	if m.eventModel {
		visit.HasEventModel, visit.EventModel, visit.EventModelEnd = true, Visit1, AssignmentOn(domain.StageGrill)
	}
	if m.grill {
		visit.Grill, visit.GrillEnd = Visit1, AssignmentOn(domain.StageSpecify)
	}
	results, err := runner.Run(visit)
	require.NoError(m.t, err)
	byCheck := map[domain.ExitCheck]domain.CheckResult{}
	for _, r := range results {
		byCheck[r.Check()] = r
	}
	require.Len(m.t, byCheck, len(domain.RequiredChecks(stage, path)), "a result for every required check")
	return byCheck
}

func passes(t *testing.T, r domain.CheckResult) {
	t.Helper()
	assert.Truef(t, r.Passed(), "%s should pass; failed: %s", r.Check(), r.Summary())
}

func fails(t *testing.T, r domain.CheckResult, contains ...string) {
	t.Helper()
	require.Falsef(t, r.Passed(), "%s should fail", r.Check())
	for _, s := range contains {
		assert.Contains(t, r.Summary(), s)
	}
	assert.LessOrEqual(t, len(r.Summary()), 300)
}

const (
	failingSpec = "package server\n\nimport \"testing\"\n\nfunc TestPort_Is8080(t *testing.T) {\n\tif Port() != 8080 {\n\t\tt.Fatalf(\"port %d\", Port())\n\t}\n}\n"
	passingSpec = "package server\n\nimport \"testing\"\n\nfunc TestPort_IsPositive(t *testing.T) {\n\tif Port() <= 0 {\n\t\tt.Fatal(\"no port\")\n\t}\n}\n"
	brokenSpec  = "package server\n\nimport \"testing\"\n\nfunc TestPort_Is8080(t *testing.T) {\n\tif Prot() != 8080 {\n\t\tt.Fatal(\"x\")\n\t}\n}\n"
	fixedCode   = "package server\n\nfunc Port() int { return 8080 }\n"
)

// Artifact written.

func TestArtifactWritten(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDiscovery)
	artifact := domain.ArtifactFor(WorkID, domain.StageDiscovery, Visit1).String()

	fails(t, m.check(domain.StageDiscovery, exitchecks.Config{})[domain.CheckArtifactWritten], artifact+" is missing")

	m.write(artifact, " \n\t\n")
	fails(t, m.check(domain.StageDiscovery, exitchecks.Config{})[domain.CheckArtifactWritten], artifact+" is empty")

	m.writeArtifact(domain.StageDiscovery)
	passes(t, m.check(domain.StageDiscovery, exitchecks.Config{})[domain.CheckArtifactWritten])
}

func TestArtifactWritten_UnchangedSinceTheBaseline_Fails(t *testing.T) {
	m := newModule(t)
	m.writeArtifact(domain.StageDiscovery) // left over from before the visit
	m.begin(domain.StageDiscovery)

	fails(t, m.check(domain.StageDiscovery, exitchecks.Config{})[domain.CheckArtifactWritten], "did not change during this stage visit")
}

// Specs fail.

func TestSpecsFail_ANewFailingTest_Passes(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/server_test.go", failingSpec)

	passes(t, m.check(domain.StageSpecify, exitchecks.Config{})[domain.CheckSpecsFail])
}

func TestSpecsFail_ANewPassingTest_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/server_test.go", passingSpec)

	fails(t, m.check(domain.StageSpecify, exitchecks.Config{})[domain.CheckSpecsFail],
		"no test written in this stage fails (TestPort_IsPositive passed)", "(log: .gofast/runtime/checks/w1-specify-1-attempt1.log)")
}

func TestSpecsFail_ANewTestThatDoesNotCompile_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/server_test.go", brokenSpec)

	fails(t, m.check(domain.StageSpecify, exitchecks.Config{})[domain.CheckSpecsFail], "specs don't compile", "undefined: Prot")
}

func TestSpecsFail_ATestFileThatDoesNotParse_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/server_test.go", "package server\n\nfunc TestX(t *testing.T) {\n")

	fails(t, m.check(domain.StageSpecify, exitchecks.Config{})[domain.CheckSpecsFail], "specs don't compile")
}

func TestSpecsFail_NoNewTestFunctions_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/helpers_test.go", "package server\n\nfunc helper() int { return 1 }\n")
	m.write("server/testdata/case.json", "{}\n")

	fails(t, m.check(domain.StageSpecify, exitchecks.Config{})[domain.CheckSpecsFail], "no test written in this stage")
}

func TestSpecsFail_AnUnrelatedPreExistingFailingTest_DoesNotCount(t *testing.T) {
	m := newModule(t)
	m.write("server/old_test.go", "package server\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) { t.Fatal(\"broken for ages\") }\n")
	m.begin(domain.StageSpecify)
	// The new test sits next to the failing old one, in the same file.
	m.write("server/old_test.go", "package server\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) { t.Fatal(\"broken for ages\") }\n\n"+
		"func TestPort_IsPositive(t *testing.T) {\n\tif Port() <= 0 {\n\t\tt.Fatal(\"no port\")\n\t}\n}\n")

	fails(t, m.check(domain.StageSpecify, exitchecks.Config{})[domain.CheckSpecsFail], "no test written in this stage fails")
}

func TestSpecsFail_AChangedExistingTestCounts(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/config_test.go", "package server\n\nimport \"testing\"\n\nfunc TestConfig(t *testing.T) {\n\tif Port() != 8080 {\n\t\tt.Fatal(\"wrong port\")\n\t}\n}\n")

	passes(t, m.check(domain.StageSpecify, exitchecks.Config{})[domain.CheckSpecsFail])
}

// Specs pass and specs untouched.

// specified is the module after a specify visit that wrote a failing spec,
// with the implement visit begun.
func specified(t *testing.T) *module {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/server_test.go", failingSpec)
	m.writeArtifact(domain.StageSpecify)
	m.begin(domain.StageImplement)
	return m
}

func TestImplement_TheFixMakesTheSpecsPass(t *testing.T) {
	m := specified(t)
	m.write("server/server.go", fixedCode)
	m.writeArtifact(domain.StageImplement)

	results := m.check(domain.StageImplement, exitchecks.Config{})

	for _, check := range domain.RequiredChecks(domain.StageImplement, Must(domain.PathFor(domain.WorkTypeFixBug))) {
		passes(t, results[check])
	}
}

func TestImplement_WithoutAFix_TheSpecsDontPass(t *testing.T) {
	m := specified(t)
	m.writeArtifact(domain.StageImplement)

	results := m.check(domain.StageImplement, exitchecks.Config{})

	fails(t, results[domain.CheckSpecsPass], "1 of 1 spec tests don't pass (TestPort_Is8080)")
	fails(t, results[domain.CheckSuitePasses], "1 test failed (TestPort_Is8080)")
	passes(t, results[domain.CheckSpecsUntouched])
	passes(t, results[domain.CheckBuildPasses])
}

func TestImplement_EditingASpecFile_FailsNamingIt(t *testing.T) {
	m := specified(t)
	m.write("server/server_test.go", passingSpec) // "fixes" the bug by changing the spec

	results := m.check(domain.StageImplement, exitchecks.Config{})

	fails(t, results[domain.CheckSpecsUntouched], "server/server_test.go (modified)")
	fails(t, results[domain.CheckSpecsPass], "TestPort_Is8080")
}

func TestImplement_DeletingASpecFile_FailsNamingIt(t *testing.T) {
	m := specified(t)
	m.remove("server/server_test.go")

	fails(t, m.check(domain.StageImplement, exitchecks.Config{})[domain.CheckSpecsUntouched], "server/server_test.go (deleted)")
}

func TestImplement_AddingAnotherTestFile_IsFine(t *testing.T) {
	m := specified(t)
	m.write("server/server.go", fixedCode)
	m.write("server/extra_test.go", passingSpec)

	results := m.check(domain.StageImplement, exitchecks.Config{})

	passes(t, results[domain.CheckSpecsUntouched])
	passes(t, results[domain.CheckSpecsPass])
}

func TestImplement_WithoutTheSpecifyBaseline_FailsWithAClearMessage(t *testing.T) {
	m := newModule(t)
	m.write("server/server_test.go", failingSpec)
	m.begin(domain.StageImplement) // specify ran on another machine
	m.write("server/server.go", fixedCode)

	results := m.check(domain.StageImplement, exitchecks.Config{})

	missing := "The baseline for 'specify' is missing (the work was started before this version of gf, or on another machine). " +
		"The owner should abandon this work with `gf abandon \"<reason>\"` and start it again."
	fails(t, results[domain.CheckSpecsPass], missing)
	fails(t, results[domain.CheckSpecsUntouched], missing)
	assert.NotContains(t, results[domain.CheckSpecsPass].Summary(), "gf extend")
}

// Build, suite and formatting.

func TestReview_ACleanModulePasses(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageReview)
	m.writeArtifact(domain.StageReview)

	for _, r := range m.check(domain.StageReview, exitchecks.Config{}) {
		passes(t, r)
	}
}

func TestReview_SummarisesWhatFailed(t *testing.T) {
	m := newModule(t)
	m.write("vendor/x/x.go", "package x\nfunc  X() {}\n")        // vendor/ is not ours to format
	m.write("server/testdata/x.go", "package x\nfunc  X() {}\n") // nor is testdata/
	m.begin(domain.StageReview)
	m.write("server/server.go", "package server\n\nfunc  Port() int { return 8081 }\n")
	m.write("server/server_test.go", failingSpec)

	results := m.check(domain.StageReview, exitchecks.Config{})

	fails(t, results[domain.CheckFormatted], "not gofmt-formatted: server/server.go (log:")
	fails(t, results[domain.CheckSuitePasses], "1 test failed (TestPort_Is8080)")
	passes(t, results[domain.CheckBuildPasses])
	log, err := os.ReadFile(filepath.Join(m.root, ".gofast", "runtime", "checks", "w1-review-1-attempt1.log"))
	require.NoError(t, err)
	assert.Contains(t, string(log), "$ go test -json ./...")
	assert.Contains(t, string(log), "port 8081", "the full test output is in the log")
}

func TestBuildPasses_ABuildFailureFails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageReview)
	m.write("server/server.go", "package server\n\nfunc Port() int { return \"8080\" }\n")

	results := m.check(domain.StageReview, exitchecks.Config{})

	fails(t, results[domain.CheckBuildPasses], "`go build ./...` failed (exit status 1): server/server.go:3:26: cannot use")
	fails(t, results[domain.CheckSuitePasses], "the suite doesn't build")
}

// Integration.

func TestIntegrationPasses(t *testing.T) {
	cases := map[string]struct {
		config   exitchecks.Config
		passed   bool
		contains []string
	}{
		"missing configuration": {exitchecks.Config{}, false,
			[]string{"No integration command is configured. The owner adds one to .gofast/checks.json"}},
		"passing command": {exitchecks.Config{Integration: &exitchecks.Command{Argv: []string{"go", "test", "./..."}, Timeout: time.Minute}}, true, nil},
		"failing command": {exitchecks.Config{Integration: &exitchecks.Command{Argv: []string{"sh", "-c", "echo boom >&2; exit 3"}, Timeout: time.Minute}}, false,
			[]string{"`sh -c echo boom >&2; exit 3` failed (exit status 3): boom (log: .gofast/runtime/checks/"}},
		"timeout": {exitchecks.Config{Integration: &exitchecks.Command{Argv: []string{"sleep", "30"}, Timeout: 200 * time.Millisecond}}, false,
			[]string{"`sleep 30` timed out after 200ms"}},
		"no GF_ACTOR": {exitchecks.Config{Integration: &exitchecks.Command{Argv: []string{"sh", "-c", `test -z "$GF_ACTOR"`}, Timeout: time.Minute}}, true, nil},
		"no terminal": {exitchecks.Config{Integration: &exitchecks.Command{Argv: []string{"sh", "-c", "! test -t 0"}, Timeout: time.Minute}}, true, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := newModule(t)
			m.begin(domain.StageIntegrationTesting)
			m.writeArtifact(domain.StageIntegrationTesting)

			started := time.Now()
			r := m.check(domain.StageIntegrationTesting, c.config)[domain.CheckIntegrationPasses]

			assert.Less(t, time.Since(started), 20*time.Second)
			if c.passed {
				passes(t, r)
			} else {
				fails(t, r, c.contains...)
			}
		})
	}
}

func TestRun_PrintsEachCheckAsItRuns(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/server_test.go", passingSpec)
	m.writeArtifact(domain.StageSpecify)
	changed, _, err := worktree.OpenBaselines(m.root).ChangedSince(AssignmentOn(domain.StageSpecify))
	require.NoError(t, err)
	var progress testWriter

	_, err = exitchecks.Runner{Root: m.root, Config: exitchecks.Config{Timeout: time.Minute}, Env: os.Environ(), Progress: &progress}.Run(exitchecks.Visit{
		Assignment: AssignmentOn(domain.StageSpecify), Work: WorkID, Stage: domain.StageSpecify, Visit: Visit1, Attempt: Attempt(2), Changed: changed,
	})

	require.NoError(t, err)
	assert.Equal(t, "✓ artifact-written\n✗ specs-fail: no test written in this stage fails (TestPort_IsPositive passed) (log: .gofast/runtime/checks/w1-specify-1-attempt2.log)\n", string(progress))
}

type testWriter []byte

func (w *testWriter) Write(p []byte) (int, error) { *w = append(*w, p...); return len(p), nil }

// A later implement visit, after a rework.

// checkImplementVisit runs implement's checks for its nth visit, whose specs
// are those of specify's first visit, which ended when implement's first
// visit began.
func (m *module) checkImplementVisit(n int) map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	id := domain.AssignmentIDFor(WorkID, domain.StageImplement, Visit(n))
	changed, _, err := worktree.OpenBaselines(m.root).ChangedSince(id)
	require.NoError(m.t, err)
	runner := exitchecks.Runner{Root: m.root, Config: exitchecks.Config{Timeout: time.Minute}, Env: os.Environ()}
	results, err := runner.Run(exitchecks.Visit{
		Assignment: id, Work: WorkID, Stage: domain.StageImplement, Visit: Visit(n), Attempt: Attempt(1),
		Specify: AssignmentOn(domain.StageSpecify), SpecifyEnd: AssignmentOn(domain.StageImplement), Changed: changed,
	})
	require.NoError(m.t, err)
	byCheck := map[domain.ExitCheck]domain.CheckResult{}
	for _, r := range results {
		byCheck[r.Check()] = r
	}
	return byCheck
}

func TestImplement_OnALaterVisit_TheSpecsAreSpecifysNotTheTestsTheFirstVisitWrote(t *testing.T) {
	m := specified(t)
	m.write("server/server.go", fixedCode)
	m.write("server/extra_test.go", passingSpec) // implement v1's own test, not a spec
	m.writeArtifact(domain.StageImplement)
	_, err := worktree.OpenBaselines(m.root).Ensure(domain.AssignmentIDFor(WorkID, domain.StageImplement, Visit(2)))
	require.NoError(t, err)
	m.write("server/extra_test.go", strings.ReplaceAll(passingSpec, "IsPositive", "IsAboveZero")) // implement v2 may change it

	results := m.checkImplementVisit(2)

	passes(t, results[domain.CheckSpecsUntouched])
	passes(t, results[domain.CheckSpecsPass])

	m.write("server/server_test.go", passingSpec) // but not specify's
	fails(t, m.checkImplementVisit(2)[domain.CheckSpecsUntouched], "server/server_test.go (modified)")
}
