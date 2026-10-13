package domain_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// A refactor maps the code first, settles intent and what must not change
// with the owner, has its plan approved, pins behaviour, and only then moves
// code.
func TestRefactorPath(t *testing.T) {
	path := must(domain.PathFor(domain.WorkTypeRefactor))
	assert.Equal(t, domain.PathV1, path.Version())
	type step struct {
		stage    domain.Stage
		gate     domain.Gate
		optional bool
	}
	var got []step
	for _, s := range path.Steps() {
		assert.Equal(t, 3, s.Budget().Int())
		got = append(got, step{s.Stage(), s.Gate(), s.Optional()})
	}
	assert.Equal(t, []step{
		{domain.StageDiscovery, domain.GateAuto, false},
		{domain.StageGrill, domain.GateHuman, false},
		{domain.StagePlan, domain.GateHuman, false},
		{domain.StageCharacterize, domain.GateAuto, false},
		{domain.StageImplement, domain.GateAuto, false},
		{domain.StageReview, domain.GateAuto, false},
		{domain.StageIntegrationTesting, domain.GateAuto, false},
		{domain.StageStressTesting, domain.GateAuto, true},
	}, got)

	assert.Equal(t, domain.WriteScopeArtifacts, domain.StagePlan.WriteScope())
	assert.Equal(t, domain.WriteScopeTestsAndArtifacts, domain.StageCharacterize.WriteScope())
	assert.Equal(t, []domain.ExitCheck{domain.CheckArtifactWritten, domain.CheckPlanValid}, domain.RequiredChecks(domain.StagePlan, path))
	assert.Equal(t, []domain.ExitCheck{domain.CheckArtifactWritten, domain.CheckCharacterizationPasses, domain.CheckCoverageSufficient},
		domain.RequiredChecks(domain.StageCharacterize, path))
	assert.Equal(t, []domain.ExitCheck{domain.CheckArtifactWritten, domain.CheckBuildPasses, domain.CheckFormatted, domain.CheckTestsUntouched, domain.CheckSuitePasses},
		domain.RequiredChecks(domain.StageImplement, path), "no specs on a refactor: it keeps every existing test instead")
	assert.Equal(t, []domain.ExitCheck{domain.CheckArtifactWritten}, domain.RequiredChecks(domain.StageDiscovery, path))
	assert.Equal(t, []domain.ExitCheck{domain.CheckArtifactWritten, domain.CheckGrillValid}, domain.RequiredChecks(domain.StageGrill, path))
}

// Refactor work starts on discovery and accepts no inputs.
func TestStartWork_Refactor(t *testing.T) {
	events, err := domain.StartWork(workID, domain.WorkTypeRefactor, description, mainBranch, activeOn(t, mainBranch))
	require.NoError(t, err)
	assert.Equal(t, domain.StageDiscovery, events[1].(domain.StageEntered).Stage)

	spec := must(domain.NewWorkInput(domain.InputSpec, must(domain.ParseRepoPath("docs/spec.md")), domain.HashOf([]byte("x"))))
	_, err = domain.StartWork(workID, domain.WorkTypeRefactor, description, mainBranch, activeOn(t, mainBranch), spec)
	assert.ErrorIs(t, err, domain.ErrInputNotAccepted)
}

func TestRouteTargets_OnRefactor(t *testing.T) {
	for _, from := range []domain.Stage{domain.StageReview, domain.StageIntegrationTesting} {
		w := toFixerFrom(t, domain.WorkTypeRefactor, domain.PathV1, from)
		assert.Equal(t, []domain.Stage{domain.StagePlan, domain.StageCharacterize, domain.StageImplement}, w.RouteTargets(), from.String())
		assert.ErrorIs(t, w.CheckRoute(domain.StageGrill), domain.ErrInvalidRoute)
		assert.ErrorIs(t, w.CheckRoute(domain.StageDiscovery), domain.ErrInvalidRoute)
	}
}

// Grill on a refactor needs no acceptance scenarios, but what must not
// change: never empty, never just "None".
func TestGrill_OnRefactor(t *testing.T) {
	doc := must(domain.ParseRepoPath("docs/decisions/2026-10-12-w1-split-wallet.md"))
	facts := domain.GrillFacts{Work: workID, WorkType: domain.WorkTypeRefactor, Decisions: domain.DefaultDecisionsDir()}
	decided := []domain.GrillDecision{{ID: "D1", Question: "Why?", Answer: "To split the service into slices."}}

	ok := domain.NewGrill(domain.GrillValues{Decisions: decided, DocPath: doc, MustNotChange: []string{"POST /wallets keeps its wire format"}})
	assert.Empty(t, ok.Problems(facts))

	missing := domain.NewGrill(domain.GrillValues{Decisions: decided, DocPath: doc})
	assert.Equal(t, []string{"a refactor's grill needs mustNotChange: what the refactor preserves (public APIs, behaviour, event names, wire formats)"},
		missing.Problems(facts))

	none := domain.NewGrill(domain.GrillValues{Decisions: decided, DocPath: doc, MustNotChange: []string{"None."}})
	assert.Equal(t, []string{"mustNotChange says None: every refactor preserves something, so name it"}, none.Problems(facts))

	empty := domain.NewGrill(domain.GrillValues{Decisions: decided, DocPath: doc, MustNotChange: []string{"the API", " "}})
	assert.Equal(t, []string{"mustNotChange item 2 is empty"}, empty.Problems(facts))

	acceptance, mustNotChange := domain.GrillRequires(domain.WorkTypeRefactor, false)
	assert.False(t, acceptance)
	assert.True(t, mustNotChange)
	acceptance, mustNotChange = domain.GrillRequires(domain.WorkTypeNewFeature, false)
	assert.True(t, acceptance)
	assert.False(t, mustNotChange)
}

// The decision document's must-not-change list agrees with the JSON: the
// same number of items, in the same order.
func TestGrill_MustNotChange_DocumentAgreesWithTheJSON(t *testing.T) {
	g := domain.NewGrill(domain.GrillValues{MustNotChange: []string{"`POST /wallets` keeps its wire format", "event names"}})
	section := "- POST /wallets keeps its wire\n  format\n- event names\n"
	doc := domain.DecisionDocument{MustNotChange: domain.ListItems(section)}
	assert.Equal(t, []string{"POST /wallets keeps its wire format", "event names"}, doc.MustNotChange)
	assert.Empty(t, g.DocumentProblems(doc, "grill-v1.json", false))

	doc.MustNotChange = []string{"event names", "POST /wallets keeps its wire format"}
	assert.Equal(t, []string{
		`must-not-change item 1 differs: the decision document says "event names", grill-v1.json says "` + "`POST /wallets` keeps its wire format" + `"`,
		`must-not-change item 2 differs: the decision document says "POST /wallets keeps its wire format", grill-v1.json says "event names"`,
	}, g.DocumentProblems(doc, "grill-v1.json", false))

	doc.MustNotChange = []string{"event names"}
	assert.Equal(t, []string{"the '## Must not change' list has 1 items, but mustNotChange in grill-v1.json has 2"}, g.DocumentProblems(doc, "grill-v1.json", false))

	assert.Equal(t, []string{"a", "b", "c"}, domain.ListItems("intro\n* a\n1. b\n2) c"))
}

func TestRefactorPlan_Problems(t *testing.T) {
	grillDoc := must(domain.ParseRepoPath("docs/decisions/2026-10-12-w1-split-wallet.md"))
	linked := "Follows [the decisions](../../../" + grillDoc.String() + ")."
	valid := domain.NewRefactorPlan(domain.RefactorPlanValues{Areas: []string{"internal/wallet/**", "internal/ledger/posting.go"},
		Moves: []domain.RefactorMove{{From: "internal/wallet/service.go", To: "internal/wallet/slices/postTransaction/"}}})
	decisions := domain.DefaultDecisionsDir()
	assert.Empty(t, valid.Problems(linked, grillDoc, decisions))
	assert.Len(t, valid.Areas(), 2)

	cases := []struct {
		name     string
		plan     domain.RefactorPlanValues
		markdown string
		grill    domain.RepoPath
		want     string
	}{
		{"no area", domain.RefactorPlanValues{}, linked, grillDoc, `the plan names no area: list the code the refactor may change as globs under "areas"`},
		{"invalid area", domain.RefactorPlanValues{Areas: []string{"/abs/**"}}, linked, grillDoc, `area "/abs/**" is not a glob of repository paths, such as internal/wallet/**`},
		{"behaviour change", domain.RefactorPlanValues{Areas: []string{"a/**"}, BehaviourChange: true}, linked, grillDoc,
			"behaviourChange is true: a refactor changes no behaviour; a change of behaviour is modify-behaviour work"},
		{"no link", domain.RefactorPlanValues{Areas: []string{"a/**"}}, "no link here", grillDoc,
			"the markdown plan doesn't link the accepted grill's decision document, " + grillDoc.String()},
		{"no grill", domain.RefactorPlanValues{Areas: []string{"a/**"}}, linked, domain.RepoPath{},
			"the work has no accepted grill whose decision document the plan could link"},
	}
	for _, c := range cases {
		assert.Equal(t, []string{c.want}, domain.NewRefactorPlan(c.plan).Problems(c.markdown, c.grill, decisions), c.name)
	}
}

// An area that matches every path would make implement's scope a check
// that can never fail; gofast's files and the decisions are not code.
func TestRefactorPlan_AreasNameCode(t *testing.T) {
	grillDoc := must(domain.ParseRepoPath("docs/decisions/2026-10-12-w1-split-wallet.md"))
	problems := func(area string) []string {
		return domain.NewRefactorPlan(domain.RefactorPlanValues{Areas: []string{area}}).Problems(grillDoc.String(), grillDoc, domain.DefaultDecisionsDir())
	}
	for _, area := range []string{"**", "**/*", "*", ".", "./...", "/", "./**", "**/"} {
		assert.Equal(t, []string{fmt.Sprintf("area %q covers the whole repository: name the code the refactor moves (e.g. internal/wallet/**)", area)},
			problems(area), area)
	}
	for _, area := range []string{".gofast/**", ".gofast", ".gofast/works/w1/plan-v1.md"} {
		assert.Equal(t, []string{fmt.Sprintf("area %q is gofast's own files, not code: name the code the refactor moves (e.g. internal/wallet/**)", area)},
			problems(area), area)
	}
	assert.Equal(t, []string{`area "docs/decisions/**" is the decisions directory, not code: name the code the refactor moves (e.g. internal/wallet/**)`},
		problems("docs/decisions/**"))
	for _, area := range []string{"internal/**", "internal/wallet/service.go", "docs/decisionsmore/**", ".gofastish/**"} {
		assert.Empty(t, problems(area), area)
	}
}

// A refactor's implement may change only the plan's areas, and add new
// files under the directories they name; every other stage keeps its
// stage's scope.
func TestWritesOf_RefactorImplement_IsNarrowedToThePlansAreas(t *testing.T) {
	path := must(domain.PathFor(domain.WorkTypeRefactor))
	plan := domain.NewRefactorPlan(domain.RefactorPlanValues{Areas: []string{"internal/wallet/**", "internal/ledger/posting.go"}})
	facts := domain.ScopeFacts{Decisions: domain.DefaultDecisionsDir(), Plan: &plan}

	_, err := domain.WritesOf(domain.StageImplement, path, workID, domain.ScopeFacts{Decisions: domain.DefaultDecisionsDir()})
	assert.ErrorIs(t, err, domain.ErrNoAcceptedPlan)

	w := must(domain.WritesOf(domain.StageImplement, path, workID, facts))
	p := func(s string) domain.RepoPath { return must(domain.ParseRepoPath(s)) }
	assert.True(t, w.Allows(p("internal/wallet/service.go"), false))
	assert.True(t, w.Allows(p("internal/wallet/slices/post/handler.go"), true))
	assert.True(t, w.Allows(p("internal/ledger/posting.go"), false))
	assert.True(t, w.Allows(p("internal/ledger/posting_split.go"), true), "a new file under a named directory: a move")
	assert.False(t, w.Allows(p("internal/ledger/journal.go"), false), "an existing file outside the areas")
	assert.False(t, w.Allows(p("cmd/server/main.go"), false))
	assert.False(t, w.Allows(p("cmd/server/new.go"), true))
	assert.True(t, w.Allows(p(".gofast/works/w1/implement-v1.md"), false))
	assert.False(t, w.Allows(p(".gofast/events.jsonl"), false))

	changed := domain.NewChangedFiles(p("internal/wallet/service.go"), p("cmd/server/main.go")).WithAdded(p("internal/ledger/posting_split.go"))
	assert.Equal(t, []domain.RepoPath{p("cmd/server/main.go")}, w.Violations(changed))
	outcome := must(domain.NewExitCheckOutcome(path, domain.ClaimFailed(reasonOf("x")), domain.CheckWrites(w, changed), nil))
	assert.Contains(t, outcome.Reason().String(), "the plan's areas (internal/wallet/**, internal/ledger/posting.go)")

	review := must(domain.WritesOf(domain.StageReview, path, workID, facts))
	assert.Empty(t, review.Areas())
	feature := must(domain.WritesOf(domain.StageImplement, must(domain.PathFor(domain.WorkTypeNewFeature)), workID, domain.ScopeFacts{Decisions: domain.DefaultDecisionsDir()}))
	assert.True(t, feature.Allows(p("cmd/server/main.go"), false))
}
