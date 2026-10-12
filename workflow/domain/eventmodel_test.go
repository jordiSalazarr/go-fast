package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// walletModel is the brief's example event model, with WalletOpened listed
// as an existing event: valid as it is. Each test changes one part of it.
func walletModel() domain.EventModelValues {
	return domain.EventModelValues{
		Events:     []domain.ModelEvent{modelEvent("TransactionPosted", domain.VisibilityInternal)},
		Commands:   []domain.ModelCommand{domain.NewModelCommand("PostTransaction", []string{"TransactionPosted"}, []string{"InsufficientFunds"})},
		ReadModels: []domain.ReadModelDesign{domain.NewReadModelDesign("WalletBalance", []string{"TransactionPosted"})},
		Slices: []domain.SliceDesign{
			slice(domain.SliceValues{Name: "PostTransaction", Kind: domain.SliceCommand, Command: "PostTransaction", Change: domain.ChangeAdded}),
			slice(domain.SliceValues{Name: "WalletBalance", Kind: domain.SliceQuery, ReadModel: "WalletBalance", Change: domain.ChangeAdded}),
		},
		Scenarios: []domain.Scenario{
			scenario("S1", "PostTransaction", []string{"WalletOpened"}, "PostTransaction", domain.ScenarioThen{Events: []string{"TransactionPosted"}}),
			scenario("S2", "PostTransaction", []string{"WalletOpened"}, "PostTransaction", domain.ScenarioThen{Rejection: "InsufficientFunds"}),
			scenario("S3", "WalletBalance", []string{"WalletOpened", "TransactionPosted"}, "",
				domain.ScenarioThen{ReadModel: "WalletBalance", State: "balance reflects the transaction"}),
		},
		ExistingEvents: []string{"WalletOpened"},
	}
}

func modelEvent(name string, v domain.Visibility) domain.ModelEvent {
	return must(domain.NewModelEvent(name, v))
}

func scenario(id, slice string, given []string, when string, then domain.ScenarioThen) domain.Scenario {
	return domain.NewScenario(domain.ScenarioValues{ID: id, Slice: slice, Given: given, When: when, Then: then})
}

func TestEventModel_TheBriefsExample_IsValid(t *testing.T) {
	assert.Empty(t, domain.NewEventModel(walletModel()).Problems())
}

// Given a valid event model changed one way, when it is checked, then it
// has exactly these problems (none when the change keeps it valid).
func TestEventModel_Problems(t *testing.T) {
	publish := func(v *domain.EventModelValues) {
		v.Events = append(v.Events, modelEvent("WalletDebited", domain.VisibilityExternal))
		v.Slices = append(v.Slices, slice(domain.SliceValues{Name: "PublishWalletDebited", Kind: domain.SliceAutomation,
			On: "TransactionPosted", Publishes: "WalletDebited", Change: domain.ChangeAdded}))
	}
	cases := []struct {
		name   string
		change func(v *domain.EventModelValues)
		want   []string
	}{
		// The structure rules moved from the design.
		{"two commands with one name", func(v *domain.EventModelValues) {
			v.Commands = append(v.Commands, domain.NewModelCommand("PostTransaction", []string{"TransactionPosted"}, nil))
		}, []string{"command name PostTransaction is used more than once"}},
		{"a snake_case event", func(v *domain.EventModelValues) {
			v.Events = append(v.Events, modelEvent("wallet_frozen", domain.VisibilityInternal))
			v.Commands = append(v.Commands, domain.NewModelCommand("FreezeWallet", []string{"wallet_frozen"}, nil))
		}, []string{`event name "wallet_frozen" is not PascalCase`}},
		{"a command without events", func(v *domain.EventModelValues) {
			v.Commands[0] = domain.NewModelCommand("PostTransaction", nil, []string{"InsufficientFunds"})
		}, []string{"command PostTransaction produces no event", "event TransactionPosted is produced by no command and published or consumed by no automation",
			`scenario S1: command PostTransaction doesn't produce "TransactionPosted"`}},
		{"a command producing an unknown event", func(v *domain.EventModelValues) {
			v.Commands[0] = domain.NewModelCommand("PostTransaction", []string{"TransactionPosted", "Posted"}, []string{"InsufficientFunds"})
		}, []string{`command PostTransaction produces event "Posted", which the event model doesn't have`}},
		{"an event nothing produces", func(v *domain.EventModelValues) {
			v.Events = append(v.Events, modelEvent("WalletFrozen", domain.VisibilityInternal))
		}, []string{"event WalletFrozen is produced by no command and published or consumed by no automation"}},
		{"a read model from an unknown event", func(v *domain.EventModelValues) {
			v.ReadModels[0] = domain.NewReadModelDesign("WalletBalance", []string{"Deposited"})
		}, []string{`read model WalletBalance is built from event "Deposited", which the event model doesn't have`}},
		{"a read model from an existing event", func(v *domain.EventModelValues) {
			v.ReadModels[0] = domain.NewReadModelDesign("WalletBalance", []string{"WalletOpened", "TransactionPosted"})
		}, nil},
		{"a query slice of an unknown read model", func(v *domain.EventModelValues) {
			v.Slices[1] = slice(domain.SliceValues{Name: "WalletBalance", Kind: domain.SliceQuery, ReadModel: "Balance", Change: domain.ChangeAdded})
		}, []string{`query slice WalletBalance reads read model "Balance", which the event model doesn't have`,
			"scenario S3: readModel WalletBalance is not the read model of slice WalletBalance"}},
		{"an automation publishing an internal event", func(v *domain.EventModelValues) {
			v.Slices = append(v.Slices, slice(domain.SliceValues{Name: "Echo", Kind: domain.SliceAutomation, On: "TransactionPosted",
				Publishes: "TransactionPosted", Change: domain.ChangeAdded}))
			v.Scenarios = append(v.Scenarios, scenario("S4", "Echo", []string{"TransactionPosted"}, "", domain.ScenarioThen{Events: []string{"TransactionPosted"}}))
		}, []string{`automation slice Echo publishes "TransactionPosted", which is not an external event of the event model`}},
		{"every slice unchanged", func(v *domain.EventModelValues) {
			for i, s := range v.Slices {
				values := s.Values()
				values.Change = domain.ChangeUnchanged
				v.Slices[i] = slice(values)
			}
		}, []string{"no slice is added, changed or removed"}},

		// Scenario ids are unique.
		{"two scenarios with one id", func(v *domain.EventModelValues) {
			v.Scenarios[1] = scenario("S1", "PostTransaction", nil, "PostTransaction", domain.ScenarioThen{Rejection: "InsufficientFunds"})
		}, []string{"scenario id S1 is used more than once"}},
		{"a scenario without an id", func(v *domain.EventModelValues) {
			v.Scenarios = append(v.Scenarios, scenario(" ", "PostTransaction", nil, "PostTransaction", domain.ScenarioThen{Events: []string{"TransactionPosted"}}))
		}, []string{"a scenario of slice PostTransaction has no id"}},
		{"a scenario id with a space", func(v *domain.EventModelValues) {
			v.Scenarios[0] = scenario("S 1", "PostTransaction", nil, "PostTransaction", domain.ScenarioThen{Events: []string{"TransactionPosted"}})
		}, []string{`scenario id "S 1" has characters other than letters, digits, '.', '_' and '-'`}},

		// Each scenario names an existing slice.
		{"a scenario of an unknown slice", func(v *domain.EventModelValues) {
			v.Scenarios = append(v.Scenarios, scenario("S4", "Refund", nil, "Refund", domain.ScenarioThen{Events: []string{"TransactionPosted"}}))
		}, []string{`scenario S4 is about slice "Refund", which the event model doesn't have`}},

		// When is the slice's command, or absent for queries.
		{"a command scenario with another command", func(v *domain.EventModelValues) {
			v.Scenarios[0] = scenario("S1", "PostTransaction", nil, "Refund", domain.ScenarioThen{Events: []string{"TransactionPosted"}})
		}, []string{`scenario S1: when must be PostTransaction, the command of slice PostTransaction, not "Refund"`}},
		{"a command scenario without a when", func(v *domain.EventModelValues) {
			v.Scenarios[0] = scenario("S1", "PostTransaction", nil, "", domain.ScenarioThen{Events: []string{"TransactionPosted"}})
		}, []string{`scenario S1: when must be PostTransaction, the command of slice PostTransaction, not ""`}},
		{"a query scenario with a when", func(v *domain.EventModelValues) {
			v.Scenarios[2] = scenario("S3", "WalletBalance", nil, "PostTransaction", domain.ScenarioThen{ReadModel: "WalletBalance", State: "x"})
		}, []string{`scenario S3: slice WalletBalance takes no command, so the scenario has no when, not "PostTransaction"`}},

		// Given events exist in the model or in existingEvents.
		{"a scenario given an unknown event", func(v *domain.EventModelValues) {
			v.Scenarios[0] = scenario("S1", "PostTransaction", []string{"WalletClosed"}, "PostTransaction", domain.ScenarioThen{Events: []string{"TransactionPosted"}})
		}, []string{`scenario S1 is given event "WalletClosed", which is neither in the event model nor in existingEvents`}},
		{"a scenario given an event without existingEvents", func(v *domain.EventModelValues) { v.ExistingEvents = nil },
			[]string{
				`scenario S1 is given event "WalletOpened", which is neither in the event model nor in existingEvents`,
				`scenario S2 is given event "WalletOpened", which is neither in the event model nor in existingEvents`,
				`scenario S3 is given event "WalletOpened", which is neither in the event model nor in existingEvents`,
			}},

		// Then has exactly one outcome the slice can have.
		{"a then with two outcomes", func(v *domain.EventModelValues) {
			v.Scenarios[0] = scenario("S1", "PostTransaction", nil, "PostTransaction",
				domain.ScenarioThen{Events: []string{"TransactionPosted"}, Rejection: "InsufficientFunds"})
		}, []string{"scenario S1: then must have exactly one of events, rejection or readModel"}},
		{"a then with no outcome", func(v *domain.EventModelValues) {
			v.Scenarios[0] = scenario("S1", "PostTransaction", nil, "PostTransaction", domain.ScenarioThen{})
		}, []string{"scenario S1: then must have exactly one of events, rejection or readModel"}},
		{"events the command doesn't produce", func(v *domain.EventModelValues) {
			v.Scenarios[0] = scenario("S1", "PostTransaction", nil, "PostTransaction", domain.ScenarioThen{Events: []string{"WalletOpened"}})
		}, []string{`scenario S1: command PostTransaction doesn't produce "WalletOpened"`}},
		{"a rejection the command doesn't declare", func(v *domain.EventModelValues) {
			v.Scenarios[1] = scenario("S2", "PostTransaction", nil, "PostTransaction", domain.ScenarioThen{Rejection: "WalletFrozen"})
		}, []string{"scenario S2: rejection WalletFrozen is not declared on command PostTransaction",
			"command slice PostTransaction has no scenario for rejection InsufficientFunds of command PostTransaction"}},
		{"a read model outcome on a command slice", func(v *domain.EventModelValues) {
			v.Scenarios = append(v.Scenarios, scenario("S4", "PostTransaction", nil, "PostTransaction", domain.ScenarioThen{ReadModel: "WalletBalance", State: "x"}))
		}, []string{"scenario S4: readModel WalletBalance is not the read model of slice PostTransaction"}},
		{"events on a query slice", func(v *domain.EventModelValues) {
			v.Scenarios[2] = scenario("S3", "WalletBalance", nil, "", domain.ScenarioThen{Events: []string{"TransactionPosted"}})
		}, []string{"scenario S3: slice WalletBalance produces no events"}},
		{"a read model outcome without a state", func(v *domain.EventModelValues) {
			v.Scenarios[2] = scenario("S3", "WalletBalance", nil, "", domain.ScenarioThen{ReadModel: "WalletBalance"})
		}, []string{"scenario S3: then describes no state of read model WalletBalance"}},
		{"an automation that publishes, with its event", func(v *domain.EventModelValues) {
			publish(v)
			v.Scenarios = append(v.Scenarios, scenario("S4", "PublishWalletDebited", []string{"TransactionPosted"}, "", domain.ScenarioThen{Events: []string{"WalletDebited"}}))
		}, nil},
		{"an automation that publishes, with another event", func(v *domain.EventModelValues) {
			publish(v)
			v.Scenarios = append(v.Scenarios, scenario("S4", "PublishWalletDebited", []string{"TransactionPosted"}, "", domain.ScenarioThen{Events: []string{"TransactionPosted"}}))
		}, []string{`scenario S4: slice PublishWalletDebited publishes WalletDebited, not "TransactionPosted"`}},

		// Every added or changed command or automation slice has scenarios,
		// one per declared rejection at least.
		{"an added command slice without scenarios", func(v *domain.EventModelValues) { v.Scenarios = v.Scenarios[2:] },
			[]string{"command slice PostTransaction has no scenario"}},
		{"a rejection without a scenario", func(v *domain.EventModelValues) {
			v.Scenarios = append(v.Scenarios[:1], v.Scenarios[2])
		}, []string{"command slice PostTransaction has no scenario for rejection InsufficientFunds of command PostTransaction"}},
		{"an added automation without scenarios", publish, []string{"automation slice PublishWalletDebited has no scenario"}},
		{"an unchanged command slice without scenarios", func(v *domain.EventModelValues) {
			v.Slices[0] = slice(domain.SliceValues{Name: "PostTransaction", Kind: domain.SliceCommand, Command: "PostTransaction", Change: domain.ChangeUnchanged})
			v.Scenarios = v.Scenarios[2:]
		}, nil},
		{"a query slice without scenarios", func(v *domain.EventModelValues) { v.Scenarios = v.Scenarios[:2] }, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := walletModel()
			c.change(&v)

			assert.Equal(t, c.want, domain.NewEventModel(v).Problems())
		})
	}
}

// Specify covers the scenarios of the slices the model adds or changes.
func TestEventModel_ScenariosToCover(t *testing.T) {
	v := walletModel()
	v.Slices[1] = slice(domain.SliceValues{Name: "WalletBalance", Kind: domain.SliceQuery, ReadModel: "WalletBalance", Change: domain.ChangeUnchanged})
	var ids []string
	for _, s := range domain.NewEventModel(v).ScenariosToCover() {
		ids = append(ids, s.ID())
	}
	assert.Equal(t, []string{"S1", "S2"}, ids)
}

func TestAssumptions_HaveStableUniqueIDs(t *testing.T) {
	m1 := must(domain.NewAssumption("M1", "a wallet is opened before any transaction"))
	assert.Empty(t, domain.AssumptionProblems([]domain.Assumption{m1, must(domain.NewAssumption("M2", "x"))}))
	assert.Equal(t, []string{"assumption id M1 is used more than once"},
		domain.AssumptionProblems([]domain.Assumption{m1, must(domain.NewAssumption("M1", "y"))}))
	for _, bad := range [][2]string{{"A1", "x"}, {"M", "x"}, {"M1", " "}} {
		_, err := domain.NewAssumption(bad[0], bad[1])
		require.ErrorIs(t, err, domain.ErrInvalidEventModel, bad[0])
	}
}

// A scenario is covered by a test written in this visit whose marker names
// it; a marker on any other test doesn't count.
func TestUncoveredScenarios(t *testing.T) {
	markers := map[string][]string{
		"TestPostTransaction_Posts":           {"S1"},
		"TestPostTransaction_RejectsAndPosts": {"S2", "S1"},
		"TestOldSpec":                         {"S3"},
	}
	assert.Equal(t, []string{"S2", "S3"}, domain.UncoveredScenarios([]string{"S1", "S2", "S3"}, []string{"TestPostTransaction_Posts"}, markers))
	assert.Equal(t, []string{"S3"}, domain.UncoveredScenarios([]string{"S1", "S2", "S3"}, []string{"TestPostTransaction_RejectsAndPosts", "TestNoMarker"}, markers),
		"TestOldSpec wasn't written in this visit")
	assert.Empty(t, domain.UncoveredScenarios(nil, nil, nil))
}
