package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

var grillDoc = must(domain.ParseRepoPath("docs/decisions/2026-10-12-w1-wallets.md"))

// walletGrill is a complete grill of work without an event model.
func walletGrill() domain.GrillValues {
	return domain.GrillValues{
		Decisions: []domain.GrillDecision{
			{ID: "D1", Question: "Can a debit take the balance below zero?", Answer: "No.", Rationale: "Wallets never lend."},
			{ID: "D2", Question: "Are refunds in scope?", Answer: "No, later.", Rationale: "They need the ledger."},
		},
		AcceptanceScenarios: []domain.AcceptanceScenario{
			{ID: "A1", Given: "a wallet with 10", When: "a debit of 20 is posted", Then: "it is rejected for insufficient funds"},
		},
		DocPath: grillDoc,
	}
}

var noEventModel = domain.GrillFacts{Work: workID, Decisions: decisions}

func withModel(assumptions ...domain.Assumption) domain.GrillFacts {
	return domain.GrillFacts{Work: workID, Decisions: decisions, HasEventModel: true, Assumptions: assumptions}
}

func TestGrill_AComplete_Grill_HasNoProblems(t *testing.T) {
	assert.Empty(t, domain.NewGrill(walletGrill()).Problems(noEventModel))
}

// Given a complete grill changed one way, when it is checked against its
// facts, then it has exactly these problems.
func TestGrill_Problems(t *testing.T) {
	m1 := must(domain.NewAssumption("M1", "a wallet is opened before any transaction"))
	m2 := must(domain.NewAssumption("M2", "amounts are whole cents"))
	cases := []struct {
		name   string
		change func(v *domain.GrillValues)
		facts  domain.GrillFacts
		want   []string
	}{
		{"an open question", func(v *domain.GrillValues) { v.OpenQuestions = []string{"Who may freeze a wallet?"} }, noEventModel,
			[]string{"1 question is still open: Who may freeze a wallet?"}},
		{"two open questions", func(v *domain.GrillValues) { v.OpenQuestions = []string{"a?", "b?"} }, noEventModel,
			[]string{"2 questions are still open: a?; b?"}},
		{"no decision", func(v *domain.GrillValues) { v.Decisions = nil }, noEventModel, []string{"grill recorded no decision"}},
		{"a repeated decision id", func(v *domain.GrillValues) { v.Decisions[1].ID = "D1" }, noEventModel,
			[]string{"decision id D1 is used more than once"}},
		{"a decision id of another form", func(v *domain.GrillValues) { v.Decisions[1].ID = "Q2" }, noEventModel,
			[]string{`decision id "Q2" is not of the form D1, D2, …`}},
		{"a decision without an answer", func(v *domain.GrillValues) { v.Decisions[0].Answer = " " }, noEventModel,
			[]string{"decision D1 needs both a question and an answer"}},
		{"no docPath", func(v *domain.GrillValues) { v.DocPath = domain.RepoPath{} }, noEventModel, []string{"docPath is missing"}},
		{"another work's document", func(v *domain.GrillValues) {
			v.DocPath = must(domain.ParseRepoPath("docs/decisions/2026-10-12-w0-wallets.md"))
		}, noEventModel, []string{"docPath docs/decisions/2026-10-12-w0-wallets.md is not this work's decision document: docs/decisions/<YYYY-MM-DD>-w1-<slug>.md"}},

		// Without an event model, the agreed behaviour has acceptance
		// scenarios.
		{"no acceptance scenario without an event model", func(v *domain.GrillValues) { v.AcceptanceScenarios = nil }, noEventModel,
			[]string{"without an event model, grill needs acceptance scenarios (A1, A2, …)"}},
		{"no acceptance scenario with an event model", func(v *domain.GrillValues) { v.AcceptanceScenarios = nil }, withModel(), nil},
		{"a repeated acceptance scenario id", func(v *domain.GrillValues) {
			v.AcceptanceScenarios = append(v.AcceptanceScenarios, domain.AcceptanceScenario{ID: "A1", Given: "g", When: "w", Then: "t"})
		}, noEventModel, []string{"acceptance scenario id A1 is used more than once"}},
		{"an acceptance scenario without a then", func(v *domain.GrillValues) { v.AcceptanceScenarios[0].Then = "" }, noEventModel,
			[]string{"acceptance scenario A1 needs a given, a when and a then"}},
		{"an acceptance scenario id of another form", func(v *domain.GrillValues) { v.AcceptanceScenarios[0].ID = "S1" }, noEventModel,
			[]string{`acceptance scenario id "S1" is not of the form A1, A2, …`}},

		// With an event model, every assumption is addressed by a decision.
		{"every assumption addressed", func(v *domain.GrillValues) {
			v.Decisions[0].Addresses = []string{"M1"}
			v.Decisions[1].Addresses = []string{"M2"}
		}, withModel(m1, m2), nil},
		{"an assumption nobody addresses", func(v *domain.GrillValues) { v.Decisions[0].Addresses = []string{"M1"} }, withModel(m1, m2),
			[]string{"assumption M2 of the event model is addressed by no decision (amounts are whole cents)"}},
		{"assumptions don't matter without an event model", func(v *domain.GrillValues) {}, domain.GrillFacts{Work: workID, Decisions: decisions, Assumptions: []domain.Assumption{m1}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := walletGrill()
			c.change(&v)

			assert.Equal(t, c.want, domain.NewGrill(v).Problems(c.facts))
		})
	}
}

// The owner approves the document, later stages read the JSON: both list
// the same decisions and acceptance scenarios.
func TestGrill_DocumentProblems(t *testing.T) {
	grill := domain.NewGrill(walletGrill()) // D1, D2; A1
	cases := []struct {
		name          string
		decisions     string
		acceptance    string
		hasEventModel bool
		addresses     []string
		want          []string
	}{
		{"the same ids", "- D1: Below zero? No.\n- D2: Refunds? Later.\n", "- A1: Given 10, when -20, then rejected.\n", false, nil, nil},
		{"heading-style ids", "### D1: Below zero?\n\nNo.\n\n### D2 — Refunds?\n\nLater.\n", "### A1\n\nGiven 10, when -20, then rejected.\n", false, nil, nil},
		{"a decision only in the JSON", "- D1: Below zero? No.\n", "- A1: x\n", false, nil,
			[]string{"decision D2 is in grill-v1.json but not in the decision document"}},
		{"a decision only in the document", "- D1: x\n- D2: y\n- D3: z\n", "- A1: x\n", false, nil,
			[]string{"decision D3 is in the decision document but not in grill-v1.json"}},
		{"another id format", "- X1: x\n", "- A1: x\n", false, nil, []string{
			"decision D1 is in grill-v1.json but not in the decision document",
			"decision D2 is in grill-v1.json but not in the decision document"}},
		{"a scenario only in the document", "- D1: x\n- D2: y\n", "- A1: x\n- A3: z\n", false, nil,
			[]string{"acceptance scenario A3 is in the decision document but not in grill-v1.json"}},
		{"a scenario only in the JSON", "- D1: x\n- D2: y\n", "", false, nil,
			[]string{"acceptance scenario A1 is in grill-v1.json but not in the decision document"}},
		{"an id inside a line doesn't count", "- D1: x, see D2\n", "- A1: x\n", false, nil,
			[]string{"decision D2 is in grill-v1.json but not in the decision document"}},
		{"an addressed assumption the entry names", "- D1: Below zero? No (M1).\n- D2: y\n", "- A1: x\n", true, []string{"M1"}, nil},
		{"an addressed assumption the entry names on a later line", "### D1\n\nBelow zero? No.\nSettles M1.\n\n### D2\n\ny\n", "- A1: x\n", true, []string{"M1"}, nil},
		{"an addressed assumption only another entry names", "- D1: Below zero? No.\n- D2: M1 again\n", "- A1: x\n", true, []string{"M1"},
			[]string{"decision D1 addresses M1, but its entry in the decision document doesn't name M1"}},
		{"assumptions don't matter without an event model", "- D1: x\n- D2: y\n", "- A1: x\n", false, []string{"M1"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := walletGrill()
			v.Decisions[0].Addresses = c.addresses
			grill = domain.NewGrill(v)
			doc := domain.ParseDecisionDocument(c.decisions, c.acceptance)

			assert.Equal(t, c.want, grill.DocumentProblems(doc, "grill-v1.json", c.hasEventModel))
		})
	}
}
