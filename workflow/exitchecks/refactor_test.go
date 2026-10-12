package exitchecks_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

var refactorPath = Must(domain.PathFor(domain.WorkTypeRefactor))

// refactorDoc is a complete decision document of a refactor.
const refactorDoc = `# Split the server

## Intent

Make the port configurable later.

## Scope

In: server/. Out: everything else.

## Decisions

- D1: Keep Port's signature? Yes.

## Glossary

None.

## Must not change

- ` + "`Port()`" + ` returns 8081
- the package name

## Open questions

None.
`

func refactorGrillJSON(mustNotChange string) string {
	return `{"decisions": [{"id": "D1", "question": "Keep Port's signature?", "answer": "Yes.", "rationale": "Callers."}],
  "mustNotChange": [` + mustNotChange + `], "openQuestions": [], "docPath": "` + grillDoc + `"}`
}

const bothItems = `"Port() returns 8081", "the package name"`

func TestGrillValid_OnARefactor(t *testing.T) {
	check := func(json, doc string) domain.CheckResult {
		t.Helper()
		m := newModule(t)
		m.grillWrites(json, doc)
		return m.checkOn(refactorPath, domain.StageGrill, exitchecks.Config{})[domain.CheckGrillValid]
	}
	passes(t, check(refactorGrillJSON(bothItems), refactorDoc))

	withoutSection := strings.Replace(refactorDoc, "## Must not change", "## Not a section", 1)
	fails(t, check(refactorGrillJSON(bothItems), withoutSection), "no '## Must not change' section")
	fails(t, check(refactorGrillJSON(""), refactorDoc), "a refactor's grill needs mustNotChange")
	fails(t, check(refactorGrillJSON(`"the package name", "Port() returns 8081"`), refactorDoc), "must-not-change item 1 differs")
	fails(t, check(refactorGrillJSON(`"Port() returns 8081"`), refactorDoc), "the '## Must not change' list has 2 items, but mustNotChange in grill-v1.json has 1")
}

// refactorPlanned has grill accepted before plan began, and writes plan's
// outcome.
func (m *module) refactorPlanned(planJSON, markdown string) {
	m.t.Helper()
	m.refactor = true
	m.write(grillPath, refactorGrillJSON(bothItems))
	m.write(grillDoc, refactorDoc)
	m.begin(domain.StagePlan)
	m.write(domain.ArtifactFor(WorkID, domain.StagePlan, Visit1).String(), markdown)
	m.write(domain.RefactorPlanFor(WorkID, Visit1).String(), planJSON)
}

const serverPlan = `{"areas": ["server/**"], "moves": [], "behaviourChange": false}`

func TestPlanValid(t *testing.T) {
	check := func(json, markdown string) domain.CheckResult {
		t.Helper()
		m := newModule(t)
		m.refactorPlanned(json, markdown)
		return m.checkOn(refactorPath, domain.StagePlan, exitchecks.Config{})[domain.CheckPlanValid]
	}
	linked := "# Plan\n\nFollows " + grillDoc + ".\n"
	passes(t, check(serverPlan, linked))
	fails(t, check(serverPlan, "# Plan\n\nNo link.\n"), "doesn't link the accepted grill's decision document, "+grillDoc)
	fails(t, check(`{"areas": [], "behaviourChange": false}`, linked), "the plan names no area")
	fails(t, check(`{"areas": ["server/**"], "behaviourChange": true}`, linked), "behaviourChange is true")
	fails(t, check(`{"areas": ["server/**"]}`, linked), `"behaviourChange" is missing`)
}

// characterized has the plan accepted before characterize began.
func (m *module) characterized(minCoverage float64) map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	return m.checkOn(refactorPath, domain.StageCharacterize, exitchecks.Config{Refactor: exitchecks.Refactor{MinCoverage: minCoverage, MinCoverageSet: minCoverage > 0}})
}

func newCharacterize(t *testing.T) *module {
	t.Helper()
	m := newModule(t)
	m.refactorPlanned(serverPlan, "# Plan\n\nFollows "+grillDoc+".\n")
	m.begin(domain.StageCharacterize)
	m.writeArtifact(domain.StageCharacterize)
	return m
}

const characterizationTest = "package server\n\nimport \"testing\"\n\nfunc TestPort_Is8081(t *testing.T) {\n\tif Port() != 8081 {\n\t\tt.Fatal(\"port changed\")\n\t}\n}\n"

func TestCharacterizationPasses(t *testing.T) {
	t.Run("no new test: coverage decides", func(t *testing.T) {
		passes(t, newCharacterize(t).characterized(0)[domain.CheckCharacterizationPasses])
	})
	t.Run("a new passing test", func(t *testing.T) {
		m := newCharacterize(t)
		m.write("server/port_test.go", characterizationTest)
		passes(t, m.characterized(0)[domain.CheckCharacterizationPasses])
	})
	t.Run("a new failing test", func(t *testing.T) {
		m := newCharacterize(t)
		m.write("server/port_test.go", failingSpec)
		fails(t, m.characterized(0)[domain.CheckCharacterizationPasses], "1 of 1 characterization tests don't pass against the current code (TestPort_Is8080)")
	})
	t.Run("a new test that doesn't compile", func(t *testing.T) {
		m := newCharacterize(t)
		m.write("server/port_test.go", brokenSpec)
		fails(t, m.characterized(0)[domain.CheckCharacterizationPasses], "the characterization tests don't compile")
	})
}

func TestCoverageSufficient(t *testing.T) {
	t.Run("report only by default", func(t *testing.T) {
		m := newCharacterize(t)
		passes(t, m.characterized(0)[domain.CheckCoverageSufficient])
		_, result, ok, err := exitchecks.ReadCoverage(m.root, AssignmentOn(domain.StageCharacterize))
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, []exitchecks.PackageCoverage{{Package: "example.com/server/server", Dir: "server", Statements: 1, Covered: 0, Percent: 0}}, result.Packages)
	})
	t.Run("below the minimum", func(t *testing.T) {
		m := newCharacterize(t)
		fails(t, m.characterized(80)[domain.CheckCoverageSufficient], "below the owner's minimum coverage of 80%: server 0%", "server 0% (0 of 1 statements)")
	})
	t.Run("the minimum met", func(t *testing.T) {
		m := newCharacterize(t)
		m.write("server/port_test.go", characterizationTest)
		passes(t, m.characterized(80)[domain.CheckCoverageSufficient])
		_, result, _, err := exitchecks.ReadCoverage(m.root, AssignmentOn(domain.StageCharacterize))
		require.NoError(t, err)
		assert.Equal(t, 100.0, result.Packages[0].Percent)
		assert.Equal(t, 80.0, result.MinCoverage)
	})
}

func TestTestsUntouched(t *testing.T) {
	check := func(change func(m *module)) domain.CheckResult {
		t.Helper()
		m := newModule(t)
		m.begin(domain.StageImplement)
		m.writeArtifact(domain.StageImplement)
		change(m)
		return m.checkOn(refactorPath, domain.StageImplement, exitchecks.Config{})[domain.CheckTestsUntouched]
	}
	passes(t, check(func(m *module) {
		m.write("server/server.go", "package server\n\nfunc Port() int { return 8000 + 81 }\n")
	}))
	passes(t, check(func(m *module) { m.write("server/new_test.go", characterizationTest) }))
	fails(t, check(func(m *module) { m.write("server/config_test.go", "package server\n") }), "server/config_test.go (modified)", "restore them")
	fails(t, check(func(m *module) { m.remove("server/config_test.go") }), "server/config_test.go (deleted)")
}
