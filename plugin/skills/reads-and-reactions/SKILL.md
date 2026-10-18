---
name: reads-and-reactions
description: How the owner builds the read side and the reactive side of a Go app, whether state-stored or event-sourced - query slices that own their read shape and never go through aggregates, read models kept the simplest way that works, reactions (automations) triggered by domain events that are idempotent, and side effects only after the change is stored (outbox). Use whenever writing a query, read model, view or report, an automation or policy ("when X happens, do Y"), a message consumer, an integration with an external system, or anything that sends, publishes or notifies.
---

# Reads and reactions

Two rules hold everything else up. **Reads never go through aggregates**: a query has its own shape, built for its use. **Reactions only happen after the fact is stored, and are safe to repeat**: nothing is sent, published or called before the change is committed, and handling the same event twice changes nothing. This is the same in state-stored and event-sourced apps; only the storage underneath differs.

## Reads: query slices own their shape

- A query slice answers one question for one use (`walletStatement`, `pendingApprovals`). It returns its own DTO with exactly the fields that use needs.
- Never load an aggregate to answer a query, never return domain types from a query, and never add fields to an aggregate because a screen needs them.
- Keep read data the simplest way that works, and move down the list only for a stated reason (performance, a shape the write tables can't give, a different store):

  | | State-stored app | Event-sourced app |
  |---|---|---|
  | 1 | query the write tables directly | a projection queried in memory or from its store |
  | 2 | a view, or a denormalised table updated in the same transaction | a projection updated in the same transaction as the append, where the store allows it |
  | 3 | a separate read model updated by a reaction (eventually consistent) | a projection updated asynchronously by a subscription |

- A read model used by more than one slice lives at bounded-context level under its own name (`walletBalances/`), with each slice declaring its own small interface over it (see code-layout).

### Eventually consistent read models

- They lag. The API or UI must cope: return the version or position it reflects, or let the writer return what it just changed.
- **Never make a write decision from a lagging read model.** Checking "balance ≥ amount" or "email unused" against a read model is a race; that rule belongs in the aggregate or a constraint (see aggregate-design).
- Updates are idempotent: upsert by key and record the last processed event ID or position, so a replayed event is a no-op.
- Each read model can be rebuilt: recomputed from the source tables, or (event-sourced only) replayed from the stream. Design it so rebuilding is a command, not an emergency.

## Reactions: when X happened, do Y

- Name each reaction `<doY>On<X>`: `awardPointsOnTransactionPosted`, `notifyOwnerOnWalletFrozen`. One reaction, one trigger, one effect.
- Trigger reactions from **domain events**, never from row changes on business tables, and never by polling a status field.
- A reaction holds no business rules of its own. It turns the fact into a command for another aggregate (through that slice's application logic), and the receiving aggregate decides. Or it performs one external effect (send an email, call a provider).
- Don't assume order across aggregates. Where order within one aggregate matters, use the event's sequence and check the current state rather than trusting arrival order.

### Idempotent by design

Events are delivered at least once, so every reaction must tolerate repeats:
- **Commands to aggregates**: derive the command's ID from the event (`transactionID := TransactionID(event.ID)`), so the receiving aggregate's business idempotency turns a repeat into "no events" (see aggregate-design).
- **External effects**: pass the event ID as the provider's idempotency key; if the provider has none, record the processed event ID in the same transaction as the effect's local record, and skip IDs already recorded.

### Failures

- Retry transient failures with bounded backoff. After that, park the event (dead-letter) and log it with full detail. Never drop it silently.
- A failed reaction never undoes the original fact. If the business needs to undo, that is a new command and a new fact (a compensation such as `RefundIssued`), decided by the domain.
- A process with several steps or deadlines (wait for payment, then ship, or cancel after 24 h) gets its own state that records which steps are done, and models the passage of time as an explicit event (`PaymentDeadlinePassed`) triggered by a scheduler. Don't chain reactions implicitly.

## Side effects after storage: the outbox

- Deciding and doing are separate. The domain returns events; nothing is sent from inside the domain or before the transaction commits.
- **State-stored apps**: in one transaction, save the new state and one outbox row per event (with the envelope; see schema-evolution). A relay reads the outbox, publishes or runs reactions, and marks rows done. Delivery is at least once, so consumers deduplicate by event ID.
- **Event-sourced apps only**: the event store is the outbox. Reactions subscribe to the stream from a stored checkpoint.
- In a single-process app, in-process reactions are fine, but drive them from what was stored (the outbox or the log), not from the in-memory event list, so a crash between commit and reaction is recovered on restart.

## In the design

- For each query: its question, its fields, how its data is kept (rows 1–3 above), and whether it's immediately or eventually consistent.
- For each reaction: its name, its trigger event, the command or effect, its idempotency key, and what happens on failure.

## Review checklist

- [ ] No query loads an aggregate or returns domain types.
- [ ] Read models are the simplest option that works, with the reason when they're not row 1.
- [ ] No write decision reads an eventually consistent read model.
- [ ] Read model updates are idempotent and rebuildable.
- [ ] Reactions are named `<doY>On<X>`, triggered by domain events, and contain no business rules.
- [ ] Every reaction has an idempotency key; repeats change nothing.
- [ ] Nothing is published, sent or called before the change is committed; outbox or stored log drives every side effect.
- [ ] Failures retry, then park and log; nothing is dropped silently; undo is a new fact.
