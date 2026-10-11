package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jordiSalazarr/go-fast/workflow/domain"
)

// walletDesign is the brief's example design: valid as it is. Each test
// changes one part of it.
func walletDesign() domain.DesignValues {
	return domain.DesignValues{
		BoundedContext: "wallets",
		Aggregates: []domain.AggregateDesign{
			domain.NewAggregateDesign("Wallet", invariants("balance never goes below zero", "a transaction is applied at most once"),
				[]string{"Open", "Frozen", "Closed"}),
		},
		Commands: []domain.CommandDesign{
			domain.NewCommandDesign("PostTransaction", "Wallet", []string{"TransactionPosted"}, []string{"InsufficientFunds", "WalletFrozen"}),
		},
		Events: []domain.EventDesign{
			eventDesign("TransactionPosted", "Wallet", domain.VisibilityInternal),
			eventDesign("WalletDebited", "Wallet", domain.VisibilityExternal),
		},
		ReadModels: []domain.ReadModelDesign{domain.NewReadModelDesign("WalletBalance", []string{"TransactionPosted"})},
		Slices: []domain.SliceDesign{
			slice(domain.SliceValues{Name: "PostTransaction", Kind: domain.SliceCommand, Command: "PostTransaction", Change: domain.ChangeAdded}),
			slice(domain.SliceValues{Name: "WalletBalance", Kind: domain.SliceQuery, ReadModel: "WalletBalance", Change: domain.ChangeAdded}),
			slice(domain.SliceValues{Name: "PublishWalletDebited", Kind: domain.SliceAutomation, On: "TransactionPosted",
				Publishes: "WalletDebited", Change: domain.ChangeAdded}),
		},
		Contracts: []domain.ContractDesign{
			contract(domain.ContractHTTP, "POST /wallets/{id}/transactions", "PostTransaction", ""),
			contract(domain.ContractEvent, "WalletDebited", "", "fields and types in prose or JSON"),
		},
	}
}

func invariants(texts ...string) []domain.Invariant {
	var out []domain.Invariant
	for _, s := range texts {
		out = append(out, must(domain.NewInvariant(s)))
	}
	return out
}

func eventDesign(name, aggregate string, v domain.Visibility) domain.EventDesign {
	return must(domain.NewEventDesign(name, aggregate, v))
}

func slice(v domain.SliceValues) domain.SliceDesign { return must(domain.NewSliceDesign(v)) }

func contract(kind domain.ContractKind, name, slice, schema string) domain.ContractDesign {
	return must(domain.NewContractDesign(kind, name, slice, schema))
}

func TestDesign_TheBriefsExample_IsValid(t *testing.T) {
	assert.Empty(t, domain.NewDesign(walletDesign()).Problems())
}

// Given a valid design changed one way, when it is checked, then it has
// exactly these problems (none when the change keeps it valid).
func TestDesign_Problems(t *testing.T) {
	cases := []struct {
		name   string
		change func(v *domain.DesignValues)
		want   []string
	}{
		// The bounded context has a name.
		{"no bounded context", func(v *domain.DesignValues) { v.BoundedContext = " " },
			[]string{"the bounded context has no name"}},

		// Names are unique within their kind.
		{"two read models with one name", func(v *domain.DesignValues) {
			v.ReadModels = append(v.ReadModels, domain.NewReadModelDesign("WalletBalance", []string{"TransactionPosted"}))
		}, []string{"read model name WalletBalance is used more than once"}},
		{"a read model and a slice share a name", func(v *domain.DesignValues) {}, nil},
		{"two contracts with one name", func(v *domain.DesignValues) {
			v.Contracts = append(v.Contracts, contract(domain.ContractEvent, "WalletDebited", "", "again"))
		}, []string{"contract name WalletDebited is used more than once"}},
		{"an aggregate without a name", func(v *domain.DesignValues) {
			v.Aggregates = append(v.Aggregates, domain.NewAggregateDesign("", invariants("x"), nil))
		}, []string{"an aggregate has no name"}},

		// Slice, command and event names are PascalCase.
		{"a camelCase command", func(v *domain.DesignValues) {
			v.Commands[0] = domain.NewCommandDesign("postTransaction", "Wallet", []string{"TransactionPosted"}, nil)
			v.Slices[0] = slice(domain.SliceValues{Name: "PostTransaction", Kind: domain.SliceCommand, Command: "postTransaction", Change: domain.ChangeAdded})
		}, []string{`command name "postTransaction" is not PascalCase`}},
		{"a snake_case event", func(v *domain.DesignValues) {
			v.Events[0] = eventDesign("transaction_posted", "Wallet", domain.VisibilityInternal)
			v.Commands[0] = domain.NewCommandDesign("PostTransaction", "Wallet", []string{"transaction_posted"}, nil)
			v.ReadModels[0] = domain.NewReadModelDesign("WalletBalance", []string{"transaction_posted"})
			v.Slices[2] = slice(domain.SliceValues{Name: "PublishWalletDebited", Kind: domain.SliceAutomation, On: "transaction_posted",
				Publishes: "WalletDebited", Change: domain.ChangeAdded})
		}, []string{`event name "transaction_posted" is not PascalCase`}},
		{"a slice name with a space", func(v *domain.DesignValues) {
			v.Slices[1] = slice(domain.SliceValues{Name: "Wallet Balance", Kind: domain.SliceQuery, ReadModel: "WalletBalance", Change: domain.ChangeAdded})
		}, []string{`slice name "Wallet Balance" is not PascalCase`}},
		{"aggregate and read model names need not be PascalCase", func(v *domain.DesignValues) {
			v.Aggregates[0] = domain.NewAggregateDesign("wallet", invariants("x"), nil)
			v.Commands[0] = domain.NewCommandDesign("PostTransaction", "wallet", []string{"TransactionPosted"}, nil)
			v.Events[0] = eventDesign("TransactionPosted", "wallet", domain.VisibilityInternal)
			v.Events[1] = eventDesign("WalletDebited", "wallet", domain.VisibilityExternal)
			v.ReadModels[0] = domain.NewReadModelDesign("wallet balance", []string{"TransactionPosted"})
			v.Slices[1] = slice(domain.SliceValues{Name: "WalletBalance", Kind: domain.SliceQuery, ReadModel: "wallet balance", Change: domain.ChangeAdded})
		}, nil},

		// Every aggregate has at least one invariant.
		{"an aggregate without invariants", func(v *domain.DesignValues) {
			v.Aggregates[0] = domain.NewAggregateDesign("Wallet", nil, []string{"Open"})
		}, []string{"aggregate Wallet has no invariant"}},

		// Every command names an existing aggregate, produces at least one
		// event, and its rejections are non-empty names.
		{"a command of an unknown aggregate", func(v *domain.DesignValues) {
			v.Commands[0] = domain.NewCommandDesign("PostTransaction", "Account", []string{"TransactionPosted"}, nil)
		}, []string{`command PostTransaction names aggregate "Account", which the design doesn't have`}},
		{"a command without events", func(v *domain.DesignValues) {
			v.Commands = append(v.Commands, domain.NewCommandDesign("FreezeWallet", "Wallet", nil, nil))
		}, []string{"command FreezeWallet produces no event"}},
		{"a command producing an unknown event", func(v *domain.DesignValues) {
			v.Commands[0] = domain.NewCommandDesign("PostTransaction", "Wallet", []string{"TransactionPosted", "Posted"}, nil)
		}, []string{`command PostTransaction produces event "Posted", which the design doesn't have`}},
		{"a command with an empty rejection", func(v *domain.DesignValues) {
			v.Commands[0] = domain.NewCommandDesign("PostTransaction", "Wallet", []string{"TransactionPosted"}, []string{"InsufficientFunds", " "})
		}, []string{"command PostTransaction has a rejection without a name"}},
		{"a command without rejections", func(v *domain.DesignValues) {
			v.Commands[0] = domain.NewCommandDesign("PostTransaction", "Wallet", []string{"TransactionPosted"}, nil)
		}, nil},

		// Every event names an existing aggregate and is produced by a
		// command, published by an automation, or consumed from outside.
		{"an event of an unknown aggregate", func(v *domain.DesignValues) {
			v.Events[1] = eventDesign("WalletDebited", "Account", domain.VisibilityExternal)
		}, []string{`event WalletDebited names aggregate "Account", which the design doesn't have`}},
		{"an event nothing produces", func(v *domain.DesignValues) {
			v.Events = append(v.Events, eventDesign("WalletFrozen", "Wallet", domain.VisibilityInternal))
		}, []string{"event WalletFrozen is produced by no command and published or consumed by no automation"}},
		{"an event from outside that an automation consumes", func(v *domain.DesignValues) {
			v.Events = append(v.Events, eventDesign("CustomerBlocked", "Wallet", domain.VisibilityInternal))
			v.Commands = append(v.Commands, domain.NewCommandDesign("FreezeWallet", "Wallet", []string{"TransactionPosted"}, nil))
			v.Slices = append(v.Slices, slice(domain.SliceValues{Name: "FreezeOnBlock", Kind: domain.SliceAutomation, On: "CustomerBlocked",
				Command: "FreezeWallet", Change: domain.ChangeAdded}))
		}, nil},

		// Every read model is built from existing events.
		{"a read model from an unknown event", func(v *domain.DesignValues) {
			v.ReadModels[0] = domain.NewReadModelDesign("WalletBalance", []string{"TransactionPosted", "Deposited"})
		}, []string{`read model WalletBalance is built from event "Deposited", which the design doesn't have`}},
		{"a read model from no event", func(v *domain.DesignValues) {
			v.ReadModels[0] = domain.NewReadModelDesign("WalletBalance", nil)
		}, []string{"read model WalletBalance is built from no event"}},

		// Every slice references what its kind needs.
		{"a command slice of an unknown command", func(v *domain.DesignValues) {
			v.Slices[0] = slice(domain.SliceValues{Name: "PostTransaction", Kind: domain.SliceCommand, Command: "Post", Change: domain.ChangeAdded})
		}, []string{`command slice PostTransaction handles command "Post", which the design doesn't have`}},
		{"a query slice of an unknown read model", func(v *domain.DesignValues) {
			v.Slices[1] = slice(domain.SliceValues{Name: "WalletBalance", Kind: domain.SliceQuery, ReadModel: "Balance", Change: domain.ChangeAdded})
		}, []string{`query slice WalletBalance reads read model "Balance", which the design doesn't have`}},
		{"an automation on an unknown event", func(v *domain.DesignValues) {
			v.Slices[2] = slice(domain.SliceValues{Name: "PublishWalletDebited", Kind: domain.SliceAutomation, On: "Posted",
				Publishes: "WalletDebited", Change: domain.ChangeAdded})
		}, []string{`automation slice PublishWalletDebited reacts to event "Posted", which the design doesn't have`}},
		{"an automation that does nothing", func(v *domain.DesignValues) {
			v.Slices = append(v.Slices, slice(domain.SliceValues{Name: "Audit", Kind: domain.SliceAutomation, On: "TransactionPosted", Change: domain.ChangeAdded}))
		}, []string{"automation slice Audit neither sends a command nor publishes an external event"}},
		{"an automation sending an unknown command", func(v *domain.DesignValues) {
			v.Slices = append(v.Slices, slice(domain.SliceValues{Name: "Audit", Kind: domain.SliceAutomation, On: "TransactionPosted",
				Command: "RecordAudit", Change: domain.ChangeAdded}))
		}, []string{`automation slice Audit sends command "RecordAudit", which the design doesn't have`}},
		{"an automation publishing an internal event", func(v *domain.DesignValues) {
			v.Slices[2] = slice(domain.SliceValues{Name: "PublishWalletDebited", Kind: domain.SliceAutomation, On: "TransactionPosted",
				Publishes: "TransactionPosted", Change: domain.ChangeAdded})
		}, []string{
			"event WalletDebited is produced by no command and published or consumed by no automation",
			`automation slice PublishWalletDebited publishes "TransactionPosted", which is not an external event of the design`,
		}},
		{"an automation sending an existing command", func(v *domain.DesignValues) {
			v.Slices = append(v.Slices, slice(domain.SliceValues{Name: "Repost", Kind: domain.SliceAutomation, On: "TransactionPosted",
				Command: "PostTransaction", Change: domain.ChangeAdded}))
		}, nil},

		// Every external event has an event contract; every http contract
		// names an existing slice.
		{"an external event without a contract", func(v *domain.DesignValues) {
			v.Contracts = v.Contracts[:1]
		}, []string{"external event WalletDebited has no event contract"}},
		{"an external event with only an http contract of its name", func(v *domain.DesignValues) {
			v.Contracts[1] = contract(domain.ContractHTTP, "WalletDebited", "PostTransaction", "")
		}, []string{"external event WalletDebited has no event contract"}},
		{"an http contract of an unknown slice", func(v *domain.DesignValues) {
			v.Contracts[0] = contract(domain.ContractHTTP, "POST /wallets/{id}/transactions", "Post", "")
		}, []string{`http contract "POST /wallets/{id}/transactions" names slice "Post", which the design doesn't have`}},

		// At least one slice changes.
		{"every slice unchanged", func(v *domain.DesignValues) {
			for i, s := range v.Slices {
				values := s.Values()
				values.Change = domain.ChangeUnchanged
				v.Slices[i] = slice(values)
			}
		}, []string{"no slice is added, changed or removed"}},
		{"only a removed slice", func(v *domain.DesignValues) {
			for i, s := range v.Slices {
				values := s.Values()
				values.Change = domain.ChangeUnchanged
				if i == 1 {
					values.Change = domain.ChangeRemoved
				}
				v.Slices[i] = slice(values)
			}
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := walletDesign()
			c.change(&v)

			problems := domain.NewDesign(v).Problems()

			assert.Equal(t, c.want, problems)
		})
	}
}

func TestDesign_ProblemsAreListedTogether(t *testing.T) {
	v := walletDesign()
	v.BoundedContext = ""
	v.Aggregates[0] = domain.NewAggregateDesign("Wallet", nil, nil)
	v.Contracts = nil

	problems := domain.NewDesign(v).Problems()

	assert.Equal(t, []string{
		"the bounded context has no name",
		"aggregate Wallet has no invariant",
		"external event WalletDebited has no event contract",
	}, problems)
}

// Given a design, when specify's tests are compared with it, then every
// added or changed command and automation slice needs a Test<SliceName>
// spec; queries and unchanged or removed slices don't.
func TestDesign_SlicesNeedingSpecs(t *testing.T) {
	v := walletDesign()
	v.Slices = append(v.Slices,
		slice(domain.SliceValues{Name: "Repost", Kind: domain.SliceAutomation, On: "TransactionPosted", Command: "PostTransaction", Change: domain.ChangeChanged}),
		slice(domain.SliceValues{Name: "OldPost", Kind: domain.SliceCommand, Command: "PostTransaction", Change: domain.ChangeUnchanged}),
		slice(domain.SliceValues{Name: "Legacy", Kind: domain.SliceCommand, Command: "PostTransaction", Change: domain.ChangeRemoved}),
	)
	design := domain.NewDesign(v)

	var need []string
	for _, s := range design.SlicesNeedingSpecs() {
		need = append(need, s.Name())
	}
	assert.Equal(t, []string{"PostTransaction", "PublishWalletDebited", "Repost"}, need)

	uncovered := func(tests ...string) []string {
		var out []string
		for _, s := range design.UncoveredSlices(tests) {
			out = append(out, s.Name())
		}
		return out
	}
	assert.Equal(t, []string{"PostTransaction", "PublishWalletDebited", "Repost"}, uncovered())
	assert.Equal(t, []string{"Repost"}, uncovered("TestPostTransaction_RejectsWhenFrozen", "TestPublishWalletDebited"))
	assert.Empty(t, uncovered("TestPostTransaction_RejectsWhenFrozen", "TestPublishWalletDebited_OnPost", "TestRepost"))
	assert.Equal(t, []string{"PostTransaction", "PublishWalletDebited", "Repost"}, uncovered("TestWalletBalance", "TestOldPost", "PostTransactionTest"))
}

func TestDesign_ValuesRejectWhatCantBeADesign(t *testing.T) {
	_, err := domain.NewInvariant("  ")
	require.ErrorIs(t, err, domain.ErrInvalidDesign)
	for _, parse := range []func(string) error{
		func(s string) error { _, err := domain.ParseVisibility(s); return err },
		func(s string) error { _, err := domain.ParseSliceKind(s); return err },
		func(s string) error { _, err := domain.ParseChange(s); return err },
		func(s string) error { _, err := domain.ParseContractKind(s); return err },
	} {
		require.ErrorIs(t, parse("public"), domain.ErrInvalidDesign)
	}
	_, err = domain.NewSliceDesign(domain.SliceValues{Name: "X", Change: domain.ChangeAdded})
	require.ErrorIs(t, err, domain.ErrMissingValue)
	_, err = domain.NewSliceDesign(domain.SliceValues{Name: "X", Kind: domain.SliceQuery})
	require.ErrorIs(t, err, domain.ErrMissingValue)
	_, err = domain.NewEventDesign("X", "Wallet", domain.Visibility{})
	require.ErrorIs(t, err, domain.ErrMissingValue)
	_, err = domain.NewContractDesign(domain.ContractKind{}, "X", "", "")
	require.ErrorIs(t, err, domain.ErrMissingValue)
}
