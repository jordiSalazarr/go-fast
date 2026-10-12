package exitchecks_test

import (
	"strings"
	"testing"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

var (
	grillPath = domain.GrillFor(WorkID, Visit1).String()
	grillDoc  = "docs/decisions/2026-10-12-" + WorkID.String() + "-wallets.md"
)

// decisionDoc is a complete decision document of work without an event
// model.
const decisionDoc = `# Wallets

## Intent

Let customers hold a balance and post transactions.

## Scope

In: posting debits and credits. Out: refunds.

## Decisions

- D1: Can a debit take the balance below zero? No: wallets never lend.

## Glossary

Wallet: a customer's balance.

## Acceptance scenarios

- A1: Given a wallet with 10, when a debit of 20 is posted, then it is rejected for insufficient funds.

## Open questions

None.
`

func grillJSON(openQuestions, scenarios, addresses string) string {
	return `{"decisions": [{"id": "D1", "question": "Can a debit take the balance below zero?", "answer": "No.", "rationale": "Wallets never lend.", "addresses": [` + addresses + `]}],
  "acceptanceScenarios": [` + scenarios + `], "openQuestions": [` + openQuestions + `], "docPath": "` + grillDoc + `"}`
}

const walletScenario = `{"id": "A1", "given": "a wallet with 10", "when": "a debit of 20 is posted", "then": "it is rejected"}`

func (m *module) checkGrill() map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	return m.checkOn(Must(domain.PathFor(domain.WorkTypeNewFeature)), domain.StageGrill, exitchecks.Config{})
}

// grillWrites begins grill and writes its outcome.
func (m *module) grillWrites(json, doc string) {
	m.t.Helper()
	m.begin(domain.StageGrill)
	m.writeArtifact(domain.StageGrill)
	m.write(grillPath, json)
	m.write(grillDoc, doc)
}

func TestGrillValid_ACompleteGrill_Passes(t *testing.T) {
	m := newModule(t)
	m.grillWrites(grillJSON("", walletScenario, ""), decisionDoc)

	results := m.checkGrill()
	passes(t, results[domain.CheckArtifactWritten])
	passes(t, results[domain.CheckGrillValid])
}

func TestGrillValid_AMissingFile_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageGrill)
	m.writeArtifact(domain.StageGrill)

	fails(t, m.checkGrill()[domain.CheckGrillValid], grillPath+" is missing")
}

func TestGrillValid_AnOpenQuestion_Fails(t *testing.T) {
	m := newModule(t)
	m.grillWrites(grillJSON(`"Who may freeze a wallet?"`, walletScenario, ""), decisionDoc)

	fails(t, m.checkGrill()[domain.CheckGrillValid], "1 problem: "+grillPath+": 1 question is still open: Who may freeze a wallet?")
}

func TestGrillValid_MissingSections_Fail(t *testing.T) {
	m := newModule(t)
	doc := strings.Replace(decisionDoc, "## Glossary\n\nWallet: a customer's balance.\n\n", "", 1)
	doc = strings.Replace(doc, "None.", "Who may freeze a wallet?", 1)
	m.grillWrites(grillJSON("", walletScenario, ""), doc)

	fails(t, m.checkGrill()[domain.CheckGrillValid], "2 problems: "+grillDoc+": no '## Glossary' section; "+grillDoc+": the '## Open questions' section must say None")
}

func TestGrillValid_WithoutAnEventModel_NeedsAcceptanceScenarios(t *testing.T) {
	m := newModule(t)
	m.grillWrites(grillJSON("", "", ""), strings.Replace(decisionDoc, "- A1: Given a wallet with 10, when a debit of 20 is posted, then it is rejected for insufficient funds.\n", "", 1))

	fails(t, m.checkGrill()[domain.CheckGrillValid],
		"without an event model, grill needs acceptance scenarios (A1, A2, …)", "the '## Acceptance scenarios' section is empty")
}

func TestGrillValid_TheDocumentMustBeThisWorks(t *testing.T) {
	m := newModule(t)
	m.grillWrites(strings.Replace(grillJSON("", walletScenario, ""), grillDoc, "docs/decisions/wallets.md", 1), decisionDoc)

	fails(t, m.checkGrill()[domain.CheckGrillValid], "docPath docs/decisions/wallets.md is not this work's decision document")
}

// With an event model, every assumption it made is addressed by a decision
// that names its id; acceptance scenarios are not needed.
func TestGrillValid_WithAnEventModel_EveryAssumptionIsAddressed(t *testing.T) {
	m := newModule(t)
	m.eventModel = true
	m.write(domain.ArtifactFor(WorkID, domain.StageEventModelling, Visit1).String(),
		"# Event model\n\n## Assumptions\n\n- M1: a wallet is opened first.\n- M2: amounts are whole cents.\n")
	doc := strings.Replace(decisionDoc, "## Acceptance scenarios\n\n- A1: Given a wallet with 10, when a debit of 20 is posted, then it is rejected for insufficient funds.\n\n", "", 1)
	doc = strings.Replace(doc, "wallets never lend.", "wallets never lend. Settles M1 and M2.", 1)
	m.grillWrites(grillJSON("", "", `"M1"`), doc)

	fails(t, m.checkGrill()[domain.CheckGrillValid], "assumption M2 of the event model is addressed by no decision (amounts are whole cents.)")

	m.write(grillPath, grillJSON("", "", `"M1", "M2"`))
	passes(t, m.checkGrill()[domain.CheckGrillValid])
}

// The probe: the JSON has D1, the document the owner approves only X1;
// and the JSON lists acceptance scenarios the document doesn't show.
func TestGrillValid_ADocumentThatDisagreesWithTheJSON_Fails(t *testing.T) {
	m := newModule(t)
	more := walletScenario + `, {"id": "A2", "given": "g", "when": "w", "then": "t"}`
	m.grillWrites(grillJSON("", more, ""), strings.Replace(decisionDoc, "- D1:", "- X1:", 1))

	fails(t, m.checkGrill()[domain.CheckGrillValid], "2 problems: "+grillDoc+": decision D1 is in grill-v1.json but not in the decision document; "+
		grillDoc+": acceptance scenario A2 is in grill-v1.json but not in the decision document")
}
