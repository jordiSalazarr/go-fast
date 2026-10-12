package exitchecks_test

import (
	"testing"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
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
