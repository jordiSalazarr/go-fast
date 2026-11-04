package exitchecks_test

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
	"github.com/jordiSalazarr/go-fast/workflow/worktree"
)

const (
	// portSpecs cover A1 with one failing test and A2 and A3 with another.
	portSpecs = `package server

import "testing"

// gofast:scenario A1
func TestPort_Is8080(t *testing.T) {
	if Port() != 8080 {
		t.Fatal("want 8080")
	}
}

// TestPort_IsTheDefault also covers two scenarios.
//
// gofast:scenario A2
// gofast:scenario A3
func TestPort_IsTheDefault(t *testing.T) {
	if Port() != 8080 {
		t.Fatal("want the default port")
	}
}
`
	// configMarker marks the existing TestConfig, which this visit didn't
	// write: its marker doesn't count.
	configMarker = "package server\n\nimport \"testing\"\n\n// gofast:scenario A3\nfunc TestConfig(t *testing.T) {}\n"
)

func (m *module) checkSpecify() map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	return m.checkOn(Must(domain.PathFor(domain.WorkTypeNewFeature)), domain.StageSpecify, exitchecks.Config{})
}

// onSpecifyAfterGrill: grill, accepted with acceptance scenarios A1 to A3,
// and specify begun.
func onSpecifyAfterGrill(t *testing.T) *module {
	m := newModule(t)
	m.grill = true
	m.write(grillPath, grillJSON("", walletScenario+`, {"id": "A2", "given": "g", "when": "w", "then": "t"}, {"id": "A3", "given": "g", "when": "w", "then": "t"}`, ""))
	m.begin(domain.StageSpecify)
	m.writeArtifact(domain.StageSpecify)
	return m
}

func TestSpecsCoverScenarios_EveryAcceptanceScenarioCovered_Passes(t *testing.T) {
	m := onSpecifyAfterGrill(t)
	m.write("server/port_test.go", portSpecs)

	passes(t, m.checkSpecify()[domain.CheckSpecsCoverScenarios])
}

func TestSpecsCoverScenarios_AnUncoveredScenario_Fails(t *testing.T) {
	m := onSpecifyAfterGrill(t)
	m.write("server/port_test.go", "package server\n\nimport \"testing\"\n\n// gofast:scenario A1\nfunc TestPort_Is8080(t *testing.T) {\n\tif Port() != 8080 {\n\t\tt.Fatal(\"want 8080\")\n\t}\n}\n")

	fails(t, m.checkSpecify()[domain.CheckSpecsCoverScenarios],
		"no spec covers: A2, A3 (mark a spec written in this stage with `// gofast:scenario <ID>` above its func)")
}

func TestSpecsCoverScenarios_AMarkerOnATestNotWrittenInThisVisit_DoesntCount(t *testing.T) {
	m := onSpecifyAfterGrill(t)
	m.write("server/port_test.go", "package server\n\nimport \"testing\"\n\n// gofast:scenario A1\n// gofast:scenario A2\nfunc TestPort_Is8080(t *testing.T) {\n\tif Port() != 8080 {\n\t\tt.Fatal(\"want 8080\")\n\t}\n}\n")
	m.write("server/config_test.go", configMarker)

	fails(t, m.checkSpecify()[domain.CheckSpecsCoverScenarios], "no spec covers: A3 (")
}

// With an event model, the scenarios to cover are the model's, of the
// slices it adds or changes.
func TestSpecsCoverScenarios_WithAnEventModel_CoversItsScenarios(t *testing.T) {
	m := newModule(t)
	m.eventModel = true
	m.write(domain.EventModelFor(WorkID, Visit1).String(), walletEventModel(t))
	m.begin(domain.StageGrill)
	m.begin(domain.StageSpecify)
	m.writeArtifact(domain.StageSpecify)
	m.write("server/port_test.go", "package server\n\nimport \"testing\"\n\n// gofast:scenario S1\n// gofast:scenario S3\nfunc TestPort_Is8080(t *testing.T) {\n\tif Port() != 8080 {\n\t\tt.Fatal(\"want 8080\")\n\t}\n}\n")

	fails(t, m.checkSpecify()[domain.CheckSpecsCoverScenarios], "no spec covers: S2 (")
}

// A spec of a scenario pins the behaviour the work changes, so it fails
// before implement: a scenario whose marked specs all pass is not covered.
func TestSpecsCoverScenarios_AScenarioOnlyAPassingSpecMarks_Fails(t *testing.T) {
	m := onSpecifyAfterGrill(t)
	m.write("server/port_test.go", `package server

import "testing"

// gofast:scenario A1
// gofast:scenario A3
func TestA1(t *testing.T) { t.Fatal("not yet") }

// gofast:scenario A2
func TestA2(t *testing.T) {}
`)

	results := m.checkSpecify()
	passes(t, results[domain.CheckSpecsFail])
	fails(t, results[domain.CheckSpecsCoverScenarios], "covered only by passing specs: A2 (TestA2); a spec of a scenario must fail before implement")
}

func TestSpecsCoverScenarios_TwoFailingSpecs_Pass(t *testing.T) {
	m := onSpecifyAfterGrill(t)
	m.write("server/port_test.go", `package server

import "testing"

// gofast:scenario A1
func TestA1(t *testing.T) { t.Fatal("not yet") }

// gofast:scenario A2
// gofast:scenario A3
func TestA2(t *testing.T) { t.Fatal("not yet") }
`)

	passes(t, m.checkSpecify()[domain.CheckSpecsCoverScenarios])
}

// One failing spec is enough: a passing spec next to it is fine.
func TestSpecsCoverScenarios_AScenarioWithAFailingAndAPassingSpec_Passes(t *testing.T) {
	m := onSpecifyAfterGrill(t)
	m.write("server/port_test.go", `package server

import "testing"

// gofast:scenario A1
// gofast:scenario A2
// gofast:scenario A3
func TestA1(t *testing.T) { t.Fatal("not yet") }

// gofast:scenario A2
func TestA2(t *testing.T) {}
`)

	passes(t, m.checkSpecify()[domain.CheckSpecsCoverScenarios])
}

// checkSpecifyRework runs specify's checks for its second visit, after its
// first visit was accepted and implement's first visit began.
func (m *module) checkSpecifyRework() map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	id := domain.AssignmentIDFor(WorkID, domain.StageSpecify, Visit(2))
	changed, _, err := worktree.OpenBaselines(m.root).ChangedSince(id)
	require.NoError(m.t, err)
	runner := exitchecks.Runner{Root: m.root, Config: exitchecks.Config{Timeout: time.Minute, Decisions: domain.DefaultDecisionsDir()}, Env: append(os.Environ(), "GF_ACTOR=agent")}
	results, err := runner.Run(exitchecks.Visit{
		Assignment: id, Work: WorkID, Path: Must(domain.PathFor(domain.WorkTypeNewFeature)), Stage: domain.StageSpecify, Visit: Visit(2), Attempt: Attempt(1),
		Specify: AssignmentOn(domain.StageSpecify), SpecifyEnd: AssignmentOn(domain.StageImplement),
		Design: Visit1, DesignEnd: AssignmentOn(domain.StageSpecify),
		Grill: Visit1, GrillEnd: AssignmentOn(domain.StageSpecify), Changed: changed,
	})
	require.NoError(m.t, err)
	byCheck := map[domain.ExitCheck]domain.CheckResult{}
	for _, r := range results {
		byCheck[r.Check()] = r
	}
	return byCheck
}

// reworkingSpecify: specify's first visit covered A1 to A3, implement made
// them pass, and a rework sent the work back to specify.
func reworkingSpecify(t *testing.T) *module {
	m := onSpecifyAfterGrill(t)
	m.write("server/port_test.go", portSpecs)
	m.begin(domain.StageImplement)
	m.write("server/server.go", "package server\n\nfunc Port() int { return 8080 }\n")
	_, err := worktree.OpenBaselines(m.root).Ensure(domain.AssignmentIDFor(WorkID, domain.StageSpecify, Visit(2)))
	require.NoError(t, err)
	m.write(domain.ArtifactFor(WorkID, domain.StageSpecify, Visit(2)).String(), "# specify\n\nRework.\n")
	return m
}

// On a rework, the scenarios the earlier specify visit covered are
// implemented: the visit needs a failing spec only for what it adds.
func TestSpecsCoverScenarios_OnARework_ScenariosAnEarlierVisitCovered_AreNotRequiredAgain(t *testing.T) {
	m := reworkingSpecify(t)
	m.write("server/cap_test.go", "package server\n\nimport \"testing\"\n\nfunc TestCap(t *testing.T) { t.Fatal(\"not yet\") }\n")

	results := m.checkSpecifyRework()

	passes(t, results[domain.CheckSpecsFail])
	passes(t, results[domain.CheckSpecsCoverScenarios])
}

// A scenario the earlier visit left uncovered still needs a spec.
func TestSpecsCoverScenarios_OnARework_AScenarioNoVisitCovered_StillFails(t *testing.T) {
	m := onSpecifyAfterGrill(t)
	m.write("server/port_test.go", "package server\n\nimport \"testing\"\n\n// gofast:scenario A1\nfunc TestPort_Is8080(t *testing.T) {\n\tif Port() != 8080 {\n\t\tt.Fatal(\"want 8080\")\n\t}\n}\n")
	m.begin(domain.StageImplement)
	m.write("server/server.go", "package server\n\nfunc Port() int { return 8080 }\n")
	_, err := worktree.OpenBaselines(m.root).Ensure(domain.AssignmentIDFor(WorkID, domain.StageSpecify, Visit(2)))
	require.NoError(t, err)
	m.write(domain.ArtifactFor(WorkID, domain.StageSpecify, Visit(2)).String(), "# specify\n\nRework.\n")
	m.write("server/cap_test.go", "package server\n\nimport \"testing\"\n\nfunc TestCap(t *testing.T) { t.Fatal(\"not yet\") }\n")

	fails(t, m.checkSpecifyRework()[domain.CheckSpecsCoverScenarios], "no spec covers: A2, A3 (")
}

// On a second rework, the scenarios the first specify visit covered count
// as covered too, not only those of the latest accepted visit.
func TestSpecsCoverScenarios_OnASecondRework_ScenariosTheFirstVisitCovered_AreNotRequiredAgain(t *testing.T) {
	m := reworkingSpecify(t)
	m.write("server/cap_test.go", "package server\n\nimport \"testing\"\n\nfunc TestCap(t *testing.T) { t.Fatal(\"not yet\") }\n")
	visit2 := domain.AssignmentIDFor(WorkID, domain.StageSpecify, Visit(2))
	implement2 := domain.AssignmentIDFor(WorkID, domain.StageImplement, Visit(2))
	_, err := worktree.OpenBaselines(m.root).Ensure(implement2)
	require.NoError(t, err)
	m.write("server/server.go", "package server\n\nfunc Port() int { return 8080 }\n\nfunc Cap() int { return 1 }\n")
	m.write("server/cap_test.go", "package server\n\nimport \"testing\"\n\nfunc TestCap(t *testing.T) {}\n")
	visit3 := domain.AssignmentIDFor(WorkID, domain.StageSpecify, Visit(3))
	_, err = worktree.OpenBaselines(m.root).Ensure(visit3)
	require.NoError(t, err)
	m.write(domain.ArtifactFor(WorkID, domain.StageSpecify, Visit(3)).String(), "# specify\n\nSecond rework.\n")
	m.write("server/limit_test.go", "package server\n\nimport \"testing\"\n\nfunc TestLimit(t *testing.T) { t.Fatal(\"not yet\") }\n")

	changed, _, err := worktree.OpenBaselines(m.root).ChangedSince(visit3)
	require.NoError(t, err)
	runner := exitchecks.Runner{Root: m.root, Config: exitchecks.Config{Timeout: time.Minute, Decisions: domain.DefaultDecisionsDir()}, Env: append(os.Environ(), "GF_ACTOR=agent")}
	results, err := runner.Run(exitchecks.Visit{
		Assignment: visit3, Work: WorkID, Path: Must(domain.PathFor(domain.WorkTypeNewFeature)), Stage: domain.StageSpecify, Visit: Visit(3), Attempt: Attempt(1),
		Specify: visit2, SpecifyEnd: implement2,
		EarlierSpecifies: []exitchecks.SpecifyVisit{{Specify: AssignmentOn(domain.StageSpecify), End: AssignmentOn(domain.StageImplement)}},
		Design:           Visit1, DesignEnd: AssignmentOn(domain.StageSpecify),
		Grill: Visit1, GrillEnd: AssignmentOn(domain.StageSpecify), Changed: changed,
	})
	require.NoError(t, err)
	byCheck := map[domain.ExitCheck]domain.CheckResult{}
	for _, r := range results {
		byCheck[r.Check()] = r
	}

	passes(t, byCheck[domain.CheckSpecsFail])
	passes(t, byCheck[domain.CheckSpecsCoverScenarios])
}
