---
name: aggregate-design
description: How to find, size, name and protect aggregates in Go, the owner's way - consistency boundaries around named business invariants, small, referenced by identity, safe under concurrent and repeated commands. Use when modelling a domain, designing or reviewing aggregates, placing commands, or deciding what to do with a rule that spans aggregates, in state-stored and event-sourced apps alike.
---

# Aggregate design

An aggregate is a **consistency boundary**: the smallest piece of state that must change atomically to keep a named set of business rules true. It is never a container for related data, and never a database table. Agents get this wrong most often by modelling tables, so start from the rules every time.

## Find the boundary from the rules

1. **List the invariants**: rules that must hold after every command, written as sentences a domain expert would sign. "A wallet's balance never goes below zero." "A transaction is applied to a wallet at most once."
2. **Group what must be true at the same instant.** Ask of each pair of rules:
   - must they be consistent immediately? → same aggregate;
   - can one lag behind the other, and does the business agree? → separate aggregates, connected by an event;
   - what is the smallest unit you can lock without breaking a rule? → that is the size.
3. **Keep only the state those rules read.** Data no rule reads (display names, descriptions, history for screens) belongs to a read model, not the aggregate.
4. **Name it after what it protects**, in the business's words.

Example: `Wallet` protects "balance never negative" and "a transaction applies at most once", so it holds the balance and the IDs of applied transactions, nothing else. The monthly statement is a read model. Loyalty points earned from a posting are a separate `LoyaltyAccount`, updated by a reaction, because the business accepts that they arrive a moment later.

## Name everything in the business language

- **Aggregate**: the business concept (`Wallet`, `Reservation`). Never `Manager`, `Info`, `Data`, `Entity`, `Record`.
- **Commands**: the business action (`PostTransaction`, `FreezeWallet`). Never `Update`, `Set…`, `Save`, `Handle`, `Process`, `Decide`, `Apply`.
- **Events**: the fact, in the past tense (`TransactionPosted`, `WalletFrozen`). An event named `WalletUpdated` or `StatusChanged` records a property change, not a business fact; find the fact.
- **Rejections**: named after the rule that refused (`InsufficientFunds`, `WalletFrozen`). Each maps to one invariant; a rejection with no invariant behind it means a rule is missing from the list.
- **States**: what the business calls them (`Open`, `Frozen`, `Closed`), each its own type (see functional-domain).

## Business rules live in the aggregate

- Every rule that decides whether a command is allowed, and what happens when it is, lives in the domain. The application layer only loads, calls the domain, stores and publishes. An `if` about a business rule in a slice or handler is a rule in the wrong place: move it.
- A command method takes the aggregate's state and already-validated inputs, and returns events or a domain error. It never loads another aggregate or calls a service.
- Validate in three places, each for its own job: the edge parses input into types, constructors reject invalid values, and the aggregate checks rules that depend on state. Only the last belongs here.

## Keep it small; reference others by identity

- Hold another aggregate's ID (`CustomerID`), never the aggregate. When a decision needs a fact from elsewhere, the application layer loads it and passes it in as a value (e.g. `ActiveWorkFact`).
- Change one aggregate per transaction by default.
- **Too big** when: it protects unrelated groups of rules; unrelated commands conflict with each other under load; a small command loads a lot of state; a collection inside it grows without bound. Split by rule group, or by lifecycle (a new `Period` per month instead of one ever-growing `Ledger`).
- **Too small** when: a rule needs two of them changed atomically, or every command touches both. Merge them.

## Rules that span two aggregates

Decide with the business whether the in-between state is acceptable. If it isn't clear, put it under `## Decisions for the owner` with a recommendation.
- **Acceptable**: eventual consistency. The first aggregate emits an event; a reaction issues a command to the second (see reads-and-reactions).
- **Must be atomic**: a domain service. A pure function takes both states and returns the events for both; the application layer stores both in one transaction. If you need this often, the boundary is wrong: revisit it.
- **Uniqueness across instances** (one account per email) is a rule over a set, not over one aggregate. Enforce it with a unique constraint or a reservation, and pass the result into the domain. Never load every instance into one aggregate to check it.

## Concurrent and repeated commands

Decide for every command that can race or be retried, and write the decision into the design.
- **Concurrent writes**: optimistic concurrency by default. Load the state with its version, store with that expected version, and on a conflict re-run load → decide → store a bounded number of times (only for conflicts, never for business errors) or report a conflict to the caller. Use a pessimistic lock only when interleaving is unacceptable and contention is high, and say why. A lost update, where two postings both read balance 100 and both write, is exactly what this prevents.
- **Repeated commands**: prefer business idempotency. If the requested outcome already holds, return no events. Reject a repeat only when it signals a real error (checking out a guest who already left). For commands without a natural outcome check, store the caller's ID in the aggregate (applied transaction IDs), or derive the aggregate's ID from business keys so retries land on the same one. A generic idempotency-key store is the last resort.

## In the design

For each aggregate write: its name, its invariants as sentences, its states, the commands it handles with the events each produces, which rejection maps to which invariant, and its concurrency and idempotency choice for each command that can race or be retried. Rules that span aggregates name the mechanism: reaction, domain service or constraint.

## Review checklist

- [ ] Each aggregate protects named invariants; none mirrors a table.
- [ ] No field that no invariant reads.
- [ ] No business `if` outside the domain.
- [ ] Other aggregates referenced by ID only.
- [ ] Business names throughout: no CRUD verbs, no `…Updated` events, no generic suffixes.
- [ ] Every rejection maps to an invariant.
- [ ] Nothing changes two aggregates in one transaction except a domain service with a stated reason.
- [ ] Every command that can race or repeat has a concurrency and idempotency choice.
