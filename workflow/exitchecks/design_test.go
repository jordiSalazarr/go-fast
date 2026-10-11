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

// walletDesign is the brief's example design, valid as it is.
func walletDesign(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/wallet-design.json")
	require.NoError(t, err)
	return string(data)
}

var designPath = domain.DesignFor(WorkID, Visit1).String()

func (m *module) checkDesign() map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	return m.checkOn(domain.WorkTypeNewFeature, domain.StageDesign, exitchecks.Config{})
}

// Design valid.

func TestDesignValid_AValidDesign_Passes(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDesign)
	m.writeArtifact(domain.StageDesign)
	m.write(designPath, walletDesign(t))

	results := m.checkDesign()

	passes(t, results[domain.CheckArtifactWritten])
	passes(t, results[domain.CheckDesignValid])
	passes(t, results[domain.CheckNFRProposalsValid])
}

func TestDesignValid_AMissingFile_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDesign)
	m.writeArtifact(domain.StageDesign)

	fails(t, m.checkDesign()[domain.CheckDesignValid], designPath+" is missing")
}

func TestDesignValid_BadJSON_Fails(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDesign)
	m.write(designPath, `{"boundedContext": "wallets",`)

	fails(t, m.checkDesign()[domain.CheckDesignValid], designPath+" has 1 problem: not valid JSON for gf")

	m.write(designPath, `{"boundedContext": "wallets", "entities": []}`)
	fails(t, m.checkDesign()[domain.CheckDesignValid], `unknown field "entities"`)
}

func TestDesignValid_RuleFailures_AreListed(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDesign)
	broken := strings.Replace(walletDesign(t), `"invariants": ["balance never goes below zero", "a transaction is applied at most once"]`, `"invariants": []`, 1)
	broken = strings.Replace(broken, `{ "kind": "event", "name": "WalletDebited", "schema": "fields and types in prose or JSON" }`,
		`{ "kind": "event", "name": "SomethingElse", "schema": "x" }`, 1)
	m.write(designPath, broken)

	fails(t, m.checkDesign()[domain.CheckDesignValid],
		designPath+" has 2 problems: aggregate Wallet has no invariant; external event WalletDebited has no event contract (log: ")
}

func TestDesignValid_UnknownKindsAndValues_AreListed(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDesign)
	broken := strings.Replace(walletDesign(t), `"visibility": "external"`, `"visibility": "public"`, 1)
	broken = strings.Replace(broken, `"kind": "query"`, `"kind": "view"`, 1)
	m.write(designPath, broken)

	fails(t, m.checkDesign()[domain.CheckDesignValid],
		`events[1] ("WalletDebited"): "visibility" must be "internal" or "external", not "public"`,
		`slices[1] ("WalletBalance"): "kind" must be "command", "query" or "automation", not "view"`)
}

func TestLoadDesign_ReturnsTheValidDesign(t *testing.T) {
	m := newModule(t)
	m.write(designPath, walletDesign(t))

	design, err := exitchecks.LoadDesign(m.root, domain.DesignFor(WorkID, Visit1))

	require.NoError(t, err)
	assert.Equal(t, "wallets", design.BoundedContext())
	assert.Len(t, design.Slices(), 3)
	assert.Equal(t, domain.SliceAutomation, design.Slices()[2].Kind())
	assert.Equal(t, "WalletDebited", design.Slices()[2].Values().Publishes)
}

// NFR proposals valid.

func TestNFRProposalsValid(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDesign)
	proposals := domain.NFRProposalsFor(WorkID, Visit1).String()

	passes(t, m.checkDesign()[domain.CheckNFRProposalsValid]) // no proposal file

	m.write(proposals, `{"nfrs": [{"id": "wallet-latency", "areas": ["wallet/**"], "operation": "POST /wallets/{id}/transactions",
		"load": {"vus": 10, "duration": "10s"}, "targets": {"p95": "150ms", "minRps": 100}}]}`)
	passes(t, m.checkDesign()[domain.CheckNFRProposalsValid])

	m.write(proposals, `{"nfrs": [{"id": "wallet-latency", "areas": ["wallet/**"], "operation": "POST /wallets/{id}/transactions",
		"load": {"vus": 10, "duration": "10s"}}]}`)
	fails(t, m.checkDesign()[domain.CheckNFRProposalsValid], proposals+`: nfrs[0] ("wallet-latency"): "targets" is required`)

	m.write(proposals, `not json`)
	fails(t, m.checkDesign()[domain.CheckNFRProposalsValid], proposals+": not valid JSON for gf")
}

// Behaviour delta.

func TestBehaviourDelta(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDiscovery)
	artifact := domain.ArtifactFor(WorkID, domain.StageDiscovery, Visit1).String()
	check := func() domain.CheckResult {
		return m.checkOn(domain.WorkTypeModifyBehaviour, domain.StageDiscovery, exitchecks.Config{})[domain.CheckBehaviourDelta]
	}

	fails(t, check(), artifact+" is missing")

	m.write(artifact, "# Discovery\n\n## Current behaviour\n\nA frozen wallet accepts debits.\n")
	fails(t, check(), artifact+": no '## Expected behaviour' section")

	m.write(artifact, "# Discovery\n\n## Current behaviour\n\n### Details\n\n## Expected behaviour\n\nA frozen wallet rejects debits.\n")
	fails(t, check(), artifact+": the '## Current behaviour' section is empty")

	m.write(artifact, "# Discovery\n\n## current behaviour\n\nA frozen wallet accepts debits.\n\n## Expected behaviour\n\n## Notes\n\nx\n")
	fails(t, check(), artifact+": the '## Expected behaviour' section is empty")

	m.write(artifact, "# Discovery\n\n## Current behaviour\n\nA frozen wallet accepts debits.\n\n## Expected behaviour\n\nA frozen wallet rejects debits.\n")
	passes(t, check())
}

func TestBehaviourDelta_IsRequiredOnlyOnModifyBehaviour(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageDiscovery)
	m.writeArtifact(domain.StageDiscovery)

	results := m.check(domain.StageDiscovery, exitchecks.Config{})

	assert.NotContains(t, results, domain.CheckBehaviourDelta)
	passes(t, results[domain.CheckArtifactWritten])
}

// Specs cover slices.

const (
	// postTransactionSpec covers the PostTransaction slice, and fails.
	postTransactionSpec = "package server\n\nimport \"testing\"\n\nfunc TestPostTransaction_RejectsWhenFrozen(t *testing.T) {\n\tif Port() != 8080 {\n\t\tt.Fatal(\"x\")\n\t}\n}\n"
	// publishSpec covers the PublishWalletDebited slice.
	publishSpec = "package server\n\nimport \"testing\"\n\nfunc TestPublishWalletDebited_OnPost(t *testing.T) {}\n"
)

// onDesignedSpecify accepts the wallet design and begins specify after it.
func (m *module) onDesignedSpecify(design string) {
	m.t.Helper()
	m.write(designPath, design)
	m.begin(domain.StageSpecify)
}

func (m *module) checkDesignedSpecify() map[domain.ExitCheck]domain.CheckResult {
	m.t.Helper()
	return m.checkOn(domain.WorkTypeNewFeature, domain.StageSpecify, exitchecks.Config{})
}

func TestSpecsCoverSlices_EveryAddedCommandAndAutomationSliceCovered_Passes(t *testing.T) {
	m := newModule(t)
	m.onDesignedSpecify(walletDesign(t))
	m.write("server/wallet_test.go", postTransactionSpec)
	m.write("server/publish_test.go", publishSpec)

	results := m.checkDesignedSpecify()

	passes(t, results[domain.CheckSpecsCoverSlices])
	passes(t, results[domain.CheckSpecsFail])
}

func TestSpecsCoverSlices_AnUncoveredSlice_FailsNamingIt(t *testing.T) {
	m := newModule(t)
	m.onDesignedSpecify(walletDesign(t))
	m.write("server/wallet_test.go", postTransactionSpec)

	fails(t, m.checkDesignedSpecify()[domain.CheckSpecsCoverSlices],
		"no spec written in this stage covers 1 slice: PublishWalletDebited (TestPublishWalletDebited…)")
}

func TestSpecsCoverSlices_UnchangedAndQuerySlices_AreNotRequired(t *testing.T) {
	m := newModule(t)
	design := strings.Replace(walletDesign(t), `"publishes": "WalletDebited", "change": "added"`, `"publishes": "WalletDebited", "change": "unchanged"`, 1)
	m.onDesignedSpecify(design)
	m.write("server/wallet_test.go", postTransactionSpec)

	passes(t, m.checkDesignedSpecify()[domain.CheckSpecsCoverSlices]) // WalletBalance is a query
}

func TestSpecsCoverSlices_ACoveringTestThatIsNotNew_DoesNotCount(t *testing.T) {
	m := newModule(t)
	m.write("server/publish_test.go", publishSpec) // written before specify began
	m.onDesignedSpecify(walletDesign(t))
	m.write("server/wallet_test.go", postTransactionSpec)

	fails(t, m.checkDesignedSpecify()[domain.CheckSpecsCoverSlices], "covers 1 slice: PublishWalletDebited")
}

func TestSpecsCoverSlices_ReadsTheDesignAsItWasAccepted(t *testing.T) {
	m := newModule(t)
	m.onDesignedSpecify(walletDesign(t))
	m.write("server/wallet_test.go", postTransactionSpec)
	// Specify drops the uncovered slice from the design: the check doesn't see it.
	m.write(designPath, strings.Replace(walletDesign(t), `"change": "added" }
  ],`, `"change": "removed" }
  ],`, 1))

	fails(t, m.checkDesignedSpecify()[domain.CheckSpecsCoverSlices], "PublishWalletDebited")
}

func TestSpecsCoverSlices_IsRequiredOnlyOnPathsWithDesign(t *testing.T) {
	m := newModule(t)
	m.begin(domain.StageSpecify)
	m.write("server/server_test.go", failingSpec)

	assert.NotContains(t, m.check(domain.StageSpecify, exitchecks.Config{}), domain.CheckSpecsCoverSlices)
}
