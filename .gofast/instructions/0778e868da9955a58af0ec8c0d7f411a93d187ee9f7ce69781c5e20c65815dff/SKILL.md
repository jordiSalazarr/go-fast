---
name: functional-domain
description: How the owner writes Go domain code - pure, business-named functions that take state and facts and return events or a domain error; one type per state so illegal transitions don't compile; state evolved from events; domain errors translated at the boundary. Use whenever writing, reviewing or fixing domain code, command handling, state machines or domain errors, in state-stored and event-sourced apps alike.
---

# Functional domain

Domain code decides; it does nothing else. Every domain function takes the current state and already-validated inputs, and returns the events that happened or a domain error. No I/O, no clock, no randomness, no logging, no `context.Context`. That is what makes the rules testable without mocks and the same in every kind of app.

## Decide: business-named, pure

- A command is a method on a state type, named for the business action, with a value receiver: `func (w OpenWallet) PostTransaction(tx TransactionID, amount Money, at time.Time) (TransactionPosted, error)`.
  - Never `Decide`, `Handle`, `Execute`, `Process`, `Apply`, `Update`.
  - Return the specific event type when there is one outcome; return a small slice of a sealed event interface only when one command can produce several facts.
- Everything non-deterministic comes in as a parameter: IDs, `at time.Time`, facts loaded from elsewhere (`ActiveWorkFact`, `ExchangeRate`). The application layer generates or loads them.
- No pointers to shared state, no mutation. Copy maps and slices before changing them; a Go map in a struct is shared across copies.
- Rules live here, not in the application layer. If a slice has an `if` about a business rule, move it into the domain.

## States: make illegal transitions impossible

- One type per state, holding only what that state needs: `OpenWallet`, `FrozenWallet`, `ClosedWallet`.
- A command exists only on the states that allow it. `FrozenWallet` has no `PostTransaction` method, so calling it doesn't compile. Don't model states as a `Status` field plus `if w.Status != Open` checks.
- Group the states in a sealed interface (an unexported marker method) so code can hold "any wallet":

  ```go
  type Wallet interface{ isWallet() }

  func (OpenWallet) isWallet()   {}
  func (FrozenWallet) isWallet() {}
  ```

- Callers that hold the interface go through one domain function that switches on the state and returns the rule's rejection for the others:

  ```go
  func PostTransaction(w Wallet, tx TransactionID, amount Money, at time.Time) (TransactionPosted, error) {
      switch w := w.(type) {
      case OpenWallet:
          return w.PostTransaction(tx, amount, at)
      case FrozenWallet:
          return TransactionPosted{}, fmt.Errorf("post %s to wallet %s: %w", tx, w.ID, ErrWalletFrozen)
      default:
          return TransactionPosted{}, fmt.Errorf("post %s: %w", tx, ErrWalletClosed)
      }
  }
  ```

  That switch is business knowledge, so it lives in the domain package, never in a slice.
- Use this for any real lifecycle. A concept with a single state needs no interface.

## Evolve: state from events

- `func Evolve(w Wallet, e Event) Wallet` applies one fact and returns the next state. It never validates and never fails: the fact already happened.
- Use the same function everywhere:
  - **State-stored apps**: the application layer evolves the loaded state with the new events and saves the result, in the same transaction as any outbox rows.
  - **Event-sourced apps only**: state is a fold of `Evolve` over the stream, starting from the initial state.
- A missing record or empty stream maps to an explicit initial state (`NoWallet`), so "does it exist" is a state like any other.

## Events

- Pure business facts in the past tense (`TransactionPosted`), carrying the data a consumer needs to act on the fact.
- No envelope fields (event ID, timestamp of storage, actor, sequence). The application layer adds those when storing.
- A sealed interface groups a concept's events, as for states.

## Errors

- Each rejection is a domain error named after the rule, declared in the domain package: `var ErrInsufficientFunds = errors.New("insufficient funds")`. Use a typed error when the caller needs data from it (`InsufficientFundsError{Balance, Requested}`).
- Wrap with context using `%w`, so callers match with `errors.Is` or `errors.As` and logs keep the full detail.
- The domain never formats a message for end users. The boundary (HTTP handler, CLI) maps domain errors to user-facing messages and status codes, and logs the full wrapped error.
- Infrastructure errors never reach the domain; the domain has no infrastructure to fail.

## Tests

Domain tests read as given, when, then: given a state (or past events), when a command runs, then these events or this error. Table-driven, no mocks, no fakes, no setup beyond building values. If a domain test needs a mock, the domain is doing I/O.

## Review checklist

- [ ] No I/O, `time.Now`, `uuid.New`, `rand`, logger or `context.Context` in the domain package.
- [ ] Command methods have business names and value receivers, and return events or a domain error.
- [ ] Each state is its own type; no command exists on a state that forbids it; no `Status` field checks.
- [ ] Evolve never fails or validates.
- [ ] Events carry no envelope fields.
- [ ] Rejections are named domain errors, wrapped with `%w`, translated to user messages only at the boundary.
- [ ] No business `if` outside the domain.
- [ ] Domain tests need no mocks.
