---
name: code-layout
description: Where Go code lives and what it may depend on, the owner's way - vertical slices per business action, hexagonal dependencies pointing at the domain, small interfaces declared by the consumer, adapters inside the slice that uses them, no shared/common/utils packages, and sharing only truly identical behaviour. Use whenever creating packages or files, placing an adapter or interface, wiring dependencies, extracting shared code, or reviewing structure.
---

# Code layout

Code is organised by **business action, not by technical layer**. Each slice holds everything one use case needs, depends only on the domain and on interfaces it declares itself, and never imports another slice. Agents default to `models/`, `services/`, `repositories/`, `handlers/`; that layout is wrong here.

## The shape

```
<bounded context>/
    domain/                     aggregates, states, events, value types, domain errors
    slices/
        postTransaction/        one business action (command) or one query
            postTransaction.go      application logic: load, call domain, store
            postTransaction_test.go
            http.go                 the slice's own adapters
            postgres.go
        walletStatement/
            ...
    eventlog/                   adapter used by several slices, named for what it is
    walletBalances/             read model used by several slices
cmd/<binary>/main.go            composition root: builds adapters, wires slices
```

- Name a slice after the business action or question (`postTransaction`, `freezeWallet`, `walletStatement`), never after a technical role.
- A slice's application logic only loads, calls the domain, stores and publishes. Business rules belong in `domain/` (see functional-domain).

## Dependencies point inward

- `domain/` imports only the standard library and its own bounded context's domain. No adapters, no slices, no infrastructure packages, no `context.Context`.
- A slice imports `domain/`, the standard library, and the bounded-context packages it needs. **Slices never import other slices.** Enforce it with a test in `slices/` that parses each slice's imports and fails on another slice's path; add one if the repo doesn't have it.
- Only `cmd/` knows every concrete type. It builds adapters and passes them into slices. No package-level globals, no `init()` wiring.

## Interfaces: small, declared by the consumer

- The slice that needs a capability declares the interface, in its own package, with only the methods it calls, usually one to three:

  ```go
  // in slices/postTransaction
  type wallets interface {
      Load(ctx context.Context, id domain.WalletID) (domain.Wallet, Version, error)
      Save(ctx context.Context, w domain.Wallet, events []domain.Event, expected Version) error
  }
  ```

- Never declare an interface next to its implementation, and never a big `Repository` with every method any slice might want.
- Accept interfaces, return concrete types. Don't add an interface until a consumer needs one; one implementation plus a test is not a reason.
- The domain needs no interfaces for infrastructure: it does no I/O.

## Adapters live with their user

- An adapter used by one slice lives inside that slice (`http.go`, `postgres.go`).
- An adapter or read model used by more than one slice moves up to the bounded-context level, under a name that says what it is (`eventlog/`, `walletBalances/`, `payments/`), and each slice still declares its own small interface over it.
- Adapters translate between the outside world and the domain. They parse primitives into value types, map domain errors to transport errors, and hold no business rules.

## Share only truly identical behaviour

- Code that looks the same is not necessarily the same. Extract it only when it is the same rule, with the same reason to change. Two slices that validate "amount positive" for different business reasons keep their own code; when one rule changes, the other must not.
- Prefer duplication over the wrong abstraction. Two or three similar lines in two slices are fine.
- When something is genuinely shared:
  - a business rule → `domain/`;
  - an adapter or read model → a named bounded-context package.
- Never create `shared/`, `common/`, `utils/`, `helpers/`, `base/` or `core/` packages. If you can't name the package after what it does, it isn't a real concept.

## Review checklist

- [ ] New code sits in a slice named for a business action, or in `domain/`, or a named bounded-context package; no layer folders.
- [ ] `domain/` imports no infrastructure, no adapters, no slices.
- [ ] No slice imports another slice; the boundaries test exists and passes.
- [ ] Every interface is declared by its consumer, with only the methods it uses.
- [ ] Adapters live in the slice that uses them, unless several slices do.
- [ ] Wiring only in `cmd/`; no globals or `init()` wiring.
- [ ] No extracted code that serves two different rules; no shared/common/utils packages.
