package exitchecks_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
	. "github.com/jordiSalazarr/go-fast/workflow/domain/domaintest"
	"github.com/jordiSalazarr/go-fast/workflow/exitchecks"
)

// walletEventModel is the brief's example event model, with WalletOpened as
// an existing event: valid as it is.
func walletEventModel(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/wallet-event-model.json")
	require.NoError(t, err)
	return string(data)
}

var (
	eventModelPath     = domain.EventModelFor(WorkID, Visit1).String()
	eventModelArtifact = domain.ArtifactFor(WorkID, domain.StageEventModelling, Visit1).String()
)

func (m *module) checkEventModel() map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	return m.checkOn(Must(domain.PathFor(domain.WorkTypeNewFeature)), domain.StageEventModelling, exitchecks.Config{})
}

func TestEventModelValid_AValidModel_Passes(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageEventModelling)
	m.write(eventModelArtifact, "# Event model\n\n## Assumptions\n\n- M1: a wallet is opened before any transaction.\n- **M2**: amounts are whole cents.\n")
	m.write(eventModelPath, walletEventModel(t))

	results := m.checkEventModel()

	passes(t, results[domain.CheckArtifactWritten])
	passes(t, results[domain.CheckEventModelValid])
}

func TestEventModelValid_AMissingFile_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageEventModelling)
	m.writeArtifact(domain.StageEventModelling)

	fails(t, m.checkEventModel()[domain.CheckEventModelValid], eventModelPath+" is missing")
}

func TestEventModelValid_BadJSON_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageEventModelling)
	m.writeArtifact(domain.StageEventModelling)
	m.write(eventModelPath, `{"events": [], "aggregates": []}`)

	fails(t, m.checkEventModel()[domain.CheckEventModelValid], eventModelPath+": not valid JSON for gf: json: unknown field \"aggregates\"")
}

// An invalid scenario fails the check, naming it.
func TestEventModelValid_AnInvalidScenario_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageEventModelling)
	m.writeArtifact(domain.StageEventModelling)
	m.write(eventModelPath, strings.Replace(walletEventModel(t), `"rejection": "InsufficientFunds"`, `"rejection": "WalletFrozen"`, 1))

	fails(t, m.checkEventModel()[domain.CheckEventModelValid], "2 problems: "+eventModelPath+": scenario S2: rejection WalletFrozen is not declared on command PostTransaction")
}

func TestEventModelValid_AnUnknownSliceKind_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageEventModelling)
	m.writeArtifact(domain.StageEventModelling)
	m.write(eventModelPath, strings.Replace(walletEventModel(t), `"kind": "query"`, `"kind": "view"`, 1))

	fails(t, m.checkEventModel()[domain.CheckEventModelValid], `slices[1] ("WalletBalance"): "kind" must be "command", "query" or "automation", not "view"`)
}

// Grill asks about each assumption by its id, so every assumption has one.
func TestEventModelValid_AnAssumptionWithoutAnID_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageEventModelling)
	m.write(eventModelArtifact, "# Event model\n\n## Assumptions\n\n- M1: a wallet is opened first.\n- amounts are whole cents.\n- M1: again.\n")
	m.write(eventModelPath, walletEventModel(t))

	fails(t, m.checkEventModel()[domain.CheckEventModelValid],
		eventModelArtifact+` ## Assumptions: the assumption "amounts are whole cents." has no id: start it with M1:, M2:, …`,
		"2 problems", "assumption id M1 is used more")
}

func TestParseAssumptions(t *testing.T) {
	assumptions, problems := exitchecks.ParseAssumptions("# x\n\n## Assumptions\n\nWe assumed:\n\n- M1: one.\n* M2. two\n\n## Next\n\n- M3: not an assumption\n")
	assert.Empty(t, problems)
	require.Len(t, assumptions, 2)
	assert.Equal(t, "M1", assumptions[0].ID())
	assert.Equal(t, "one.", assumptions[0].Text())
	assert.Equal(t, "two", assumptions[1].Text())

	assumptions, problems = exitchecks.ParseAssumptions("# x\n\n## Assumptions\n\nNone.\n")
	assert.Empty(t, assumptions)
	assert.Empty(t, problems)
}
