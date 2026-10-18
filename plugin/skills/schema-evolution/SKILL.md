---
name: schema-evolution
description: How the owner keeps data contracts evolvable in Go - internal vs external events, the envelope, a hybrid schema of mandatory core plus optional fields, non-breaking changes to events, messages and HTTP APIs, versioning when a change must break, expand/contract table migrations, and upcasting for event-sourced apps. Use whenever adding or changing an event, message, API request/response, stored record or table, publishing an integration event, or reviewing a change to any serialized shape.
---

# Schema evolution

Anything stored or sent is a **contract with readers you don't control**: other services, older and newer deployments of your own code, data written years ago. Every change must keep them working. Default to additive changes; a breaking change needs a new version and a plan, and on a published contract it is the owner's decision.

## Internal vs external events

- **Internal (domain) events** are the facts your bounded context uses: pure business data, shaped by the domain, free to evolve with the code. Once stored, they follow the rules below like any other stored data.
- **External (integration) events** are published contracts for other contexts. They are their own types, often thinner, named and shaped for consumers, and versioned. An adapter maps internal → external at the boundary. Never publish a domain event type directly, and never put domain value types in an external contract.
- Decide for every event whether it is published. Design marks this as the event's visibility.

## The envelope

The domain emits pure facts. The application layer wraps each one when storing or publishing:

| Field | Meaning |
|---|---|
| `id` | unique per event, the consumers' deduplication key |
| `type` | stable name of the fact (`wallet.transaction-posted`) |
| `schemaVersion` | version of the payload's shape |
| `occurredAt` | when the fact happened, from the domain's `at` |
| `recordedAt` | when it was stored |
| `actor`, `correlationId`, `causationId` | who caused it, and the chain of messages it belongs to |
| `sequence` / stream version | position, where ordering matters |

The payload is the fact. Metadata never goes into the payload, and payload never goes into the envelope.

## Hybrid schema: mandatory core, optional rest

- **Core fields** are what the fact cannot exist without: identities and the essential data (`walletId`, `transactionId`, `amountMinor`, `currency`). They are always present, and their meaning never changes.
- **Optional fields** add detail. Consumers must work when they are absent, and producers may add more at any time.
- **Readers are tolerant**: they ignore unknown fields and unknown event types, and handle unknown enum values with a default branch. In Go, don't use `DisallowUnknownFields` when decoding a contract. Use a pointer or an explicit presence flag where an absent optional field and its zero value mean different things.

## What breaks and what doesn't

| Safe in place | Breaking: needs a new version |
|---|---|
| add an optional field | rename or remove a field |
| add a new event or message type | change a field's type, unit or meaning |
| add an endpoint, or an optional request field | make an optional field required, or add a required one |
| add an enum value, if every reader has a default branch | split or merge events |

For a breaking change:
- Publish the new version alongside the old (`schemaVersion: 2`, or a new type name when the fact itself changed). Keep the old one until its consumers have moved; that cut-off is the owner's decision.
- Never silently change what an existing field means. If the meaning changes, it is a new field.

## Shapes that age well

- Keep serialized types (DTOs) in the adapters, separate from domain types, with explicit, stable JSON names. Mapping happens in one place per version.
- Make units explicit in names (`amountMinor` plus `currency`, `timeoutSeconds`). Timestamps are RFC 3339 in UTC. IDs are strings.
- Never serialize Go-specific encodings: no `time.Duration` nanoseconds, no `iota` integers for enums (use strings).

## HTTP contracts

The same rules apply to requests and responses. Additive changes go in place; a breaking change gets a new version (a path prefix or a media type, following the repo's existing choice) while the old one keeps working. Errors have one consistent shape, with a stable machine-readable code next to the human message (see functional-domain for mapping domain errors at the boundary).

## State-stored apps: migrations

- Each migration must work with the code version currently running, so deploys can roll. Use **expand/contract**:
  1. Expand: add the new column or table, nullable or with a default.
  2. Deploy code that writes both shapes.
  3. Backfill.
  4. Switch reads to the new shape.
  5. Contract: remove the old column in a later release.
- Never rename or drop in one step. Migrations are forward-only, versioned, and live in the repo.

## Event-sourced apps only

- Stored events are immutable. Never edit them in place; a real fix is an explicit copy-and-transform migration into new streams.
- **Upcasting**: when reading, an adapter transforms each old version into the current shape, one pure function per step (v1 → v2 → v3). The domain only ever sees the current shape.
- Optional additions need no upcaster: the tolerant read handles them.

## Tests

Keep a serialized fixture of every version of every contract. Tests decode each old fixture and assert the result; published events also get tests of the shape they encode. A removed fixture is a removed promise, so only delete one together with the version it covers.

## In the design

For each contract the change touches, write: internal or external; additive or breaking; and for a breaking change, the new version, how old readers and data are handled (migration or upcaster), and how long the old version lives. A breaking change to a published contract goes under `## Decisions for the owner`.

## Review checklist

- [ ] No domain event published directly; external events are separate, mapped types.
- [ ] Envelope metadata stays out of the payload.
- [ ] Core fields unchanged in meaning; new fields optional; readers tolerate unknown fields, types and enum values.
- [ ] Every breaking change has a new version, and the old one still works.
- [ ] Units explicit; RFC 3339 UTC times; no Go-specific encodings.
- [ ] Migrations follow expand/contract; nothing renamed or dropped in one step.
- [ ] Old fixtures still decode.
