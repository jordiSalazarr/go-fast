---
name: specify
description: How the owner turns agreed behaviour into executable Go specs - one rule per test, written as given/when/then in business language, from rules to concrete examples (happy path, every rejection, boundaries, repeats), asserting only observable outcomes, and failing now for the right reason. Use when writing specs or acceptance tests before implementation, reproducing a bug as a failing test, turning scenarios or acceptance criteria into tests, and in the gofast specify stage.
---

# Specify

A spec pins **one business rule** through code that exists, in words the domain expert would recognise, and **fails now because the behaviour is missing**. Specs are the agreed behaviour made executable: implement is done when they pass, and later they are the living documentation of the rules. Capture the core behaviour and its rules, not the implementation.

## From agreement to examples

Work like an example-mapping session: rules first, then concrete examples for each rule.

1. **Collect the rules** from what was agreed:
   - the grill decision document's scenarios (`A1…`) or the event model's scenarios (`S1…`);
   - the design's invariants, commands and rejections;
   - on a bug, discovery's reproduction and the behaviour that should have happened.
2. **For each rule, pick the examples that pin it:**
   - the main success case;
   - every rejection the rule implies, each in its own spec;
   - boundaries: just inside and just outside a limit (balance exactly equal to the amount, one cent short);
   - repeats: the same command or event twice, where design gave it an idempotency choice;
   - concurrency, where design or the risk is concurrent writes.
3. **Don't invent behaviour.** If a rule has no clear answer for an example (what happens to a refund on a frozen wallet?), don't guess in a test. Put it under `## Questions for the owner` in the artifact, with the options and a recommendation. Specify is a human gate, so he answers before implement starts.

## The shape of a spec

- **One behaviour per test.** One `When`. If you need a second action, it is a second spec.
- **Declarative, not scripted.** The Given states the situation in domain terms (`frozenWallet`, `walletWithBalance(eur(100))`), not the steps to build it. Hide setup in small builders with sensible defaults, and show only the data that matters to this rule.
- **Independent.** Each test builds its own state. No shared mutable fixtures, no order between tests.
- **Observable outcomes only.** Then asserts on what the business can see: the events returned, the domain error (`errors.Is`), the stored state or outbox, the read model's answer, the HTTP status and body. Never on private fields, call counts or which internal function ran.
- **Deterministic.** Time, IDs and randomness are fixed values passed in. No `time.Now`, no sleeps.
- **Rule written above the test**, as one plain sentence; it is what the reader checks the test against.

```go
// gofast:scenario A2
// Rule: a frozen wallet accepts no postings.
func TestPostTransaction_RejectsWhenWalletFrozen(t *testing.T) {
    // Given
    wallet := frozenWallet(t)

    // When
    _, err := domain.PostTransaction(wallet, txID("tx-1"), eur(t, 10_00), at)

    // Then
    require.ErrorIs(t, err, domain.ErrWalletFrozen)
}
```

- **Names**: `Test<SliceName>_<Outcome>When<Condition>`, describing the correct behaviour (`TestPostTransaction_AppliesEachTransactionOnce`), never the bug or a ticket (`TestBug123`). On design paths, the `Test<SliceName>` prefix is what links a spec to its slice.
- **Markers**: put `// gofast:scenario <ID>` above each spec that covers an agreed scenario; one scenario may have several specs.
- Use a table only when the examples of **one** rule differ just by data (several amounts at the boundary). Different rules are different tests.

## Pick the right level

| Level | Given | When | Then | Use for |
|---|---|---|---|---|
| Domain | a state, or past events evolved into one | a domain command | events, or a domain error | every business rule |
| Slice | stored state in an in-memory fake of the slice's interface | the slice's entry point | stored state, events, outbox, the error the caller sees | orchestration, version conflicts, repeated commands |
| Reaction | an event, delivered twice | the reaction runs | one command issued, or one effect | idempotency and triggers |
| Query | events or rows | the query runs | the read shape | read models |
| Boundary | a running handler (`httptest`) | one request | status and body | each HTTP or message contract |

Rules belong at the domain level. Use slice and boundary specs for what only they can show. Fakes implement the slice's own small interface; never use mocks that assert on calls.

## Fails now, for the right reason

- Run the specs and read every failure. Each must fail on its **assertion**, with a message showing the expected and the actual behaviour. A compile error, a panic in setup or a nil dereference in the test itself is the wrong reason; fix the spec.
- A spec that passes before implement pins nothing. Either the behaviour already exists (then it isn't part of this change; drop the spec) or the assertion is too weak. Fix it.
- **When the code a spec needs doesn't exist yet**, write **skeletons** so the spec compiles: new non-test `.go` files that declare only the types, constants and function signatures the design names, each body a single `return` of zero values, plus `errSkeleton` where the signature returns an error:

  ```go
  // in a new file domain/wallet_freeze.go
  var errSkeleton = errors.New("skeleton: not implemented")

  type FrozenWallet struct{ ID WalletID }

  func (w OpenWallet) Freeze(reason FreezeReason, at time.Time) (WalletFrozen, error) {
      return WalletFrozen{}, errSkeleton
  }
  ```

  - Only new files, never edits to existing production files; no logic, no other statements. `gf` checks this (`skeletons-only`), and implement must fill every skeleton (`skeletons-filled`).
  - Declare `errSkeleton` once per package, in a skeleton file.
  - Name and place them as the design says, so implement completes them instead of moving them.
  - A spec that fails because a skeleton returns zero values or `errSkeleton` fails for the right reason: the behaviour is missing. Never use `panic` in a skeleton; it aborts the package's other tests.
- **Bugs**: reproduce at the lowest level where the bug shows. For a race, force the interleaving deterministically (for example, a fake that returns a version conflict on the first save) rather than relying on goroutines and timing; add a `-race` test only when no deterministic form exists.

## Avoid

- Scripted steps, several Whens, or one test checking many rules.
- Assertions on internals or mocks; tests that break when the code is refactored without changing behaviour.
- Big fixtures, or data that doesn't affect the rule.
- Tests for private functions.
- Vague Thens ("no error", "not nil") when the rule says what should happen.

## Artifact

```markdown
## Specs
| Scenario | Rule | Test | Level | Fails with |
|---|---|---|---|---|
| A2 | a frozen wallet accepts no postings | domain/wallet_test.go TestPostTransaction_RejectsWhenWalletFrozen | domain | expected ErrWalletFrozen, got nil |

## Rules without a spec
<rule, and why: behaviour that already exists, or can't be tested here>

## Skeletons
<new files and what they declare, for implement to fill>

## Questions for the owner
<examples the agreement doesn't settle: options and a recommendation>
```

Omit empty sections, except Specs.
