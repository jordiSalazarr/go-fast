---
name: value-types
description: How the owner models values in Go - no primitive obsession; each domain concept is its own type, valid by construction, immutable and comparable; input parsed into types at the edge; functional options for complex construction. Use whenever writing, reviewing or fixing domain types, IDs, money, quantities, constructors, request parsing, or a constructor with optional settings.
---

# Value types

A domain concept is never a bare `string`, `int` or `float64`. If a value has a rule, a unit, or could be confused with another value of the same primitive, it gets its own type, and once it exists it is valid. Agents default to primitives; that is the habit to break.

## When a value earns a type

Give it a type when any of these holds:
- it has a rule (an email has a format, a quantity is positive);
- it has a unit or currency (money, durations in business terms, percentages);
- it could be swapped with another value of the same primitive (`WalletID` and `CustomerID` are both strings; a function taking two of them should not compile with them reversed).

Leave loop counters, indices and purely technical values as primitives. A type with no rule, no unit and no confusion risk is noise.

## Valid by construction

- Use a struct with an unexported field for any type with a rule, so code outside the package can't build an invalid one:

  ```go
  type Email struct{ value string }

  func ParseEmail(raw string) (Email, error) {
      raw = strings.TrimSpace(raw)
      if !emailPattern.MatchString(raw) {
          return Email{}, fmt.Errorf("email %q: %w", raw, ErrInvalidEmail)
      }
      return Email{value: strings.ToLower(raw)}, nil
  }

  func (e Email) String() string { return e.value }
  ```

- A named type (`type WalletID string`) is acceptable only when every value of the primitive is valid, because anyone can convert to it.
- Name constructors `Parse…` when they take raw input, `New…` when they assemble already-typed parts. They return `(T, error)` with a named domain error wrapped with `%w`.
- Normalise in the constructor (trim, lower-case, round to minor units), so equal concepts compare equal.
- Never re-validate a value type inside domain functions: having one is the proof.

## Zero values

Decide what the zero value means for every type:
- either it is a meaningful default (`Quantity{}` is zero items), or
- it is "not set": give the type `IsZero() bool`, and have domain functions reject it where the value is required.

Never let a zero value pass as a valid one by accident; `Email{}` is not an email.

## Behaviour and immutability

- Put the operations the business uses on the type, with value receivers, returning new values: `func (m Money) Add(o Money) (Money, error)` fails on a currency mismatch.
- Money is an integer amount in minor units plus a currency. Never a float.
- Keep value types comparable (no slices, maps or funcs inside), so `==` and map keys work.

## Parse at the edge

- Requests, messages and rows arrive as primitives. The adapter parses them into value types, then builds the command, collecting every parse error to report together.
- The domain only ever sees value types. A domain function taking `string` for a concept is primitive obsession.
- Mapping to and from primitives for storage or transport lives in adapters. Don't put JSON or DB tags on domain types; if a type implements `UnmarshalText`, it must go through its constructor.

## Functional options for complex construction

Use them when a constructor has required parameters plus several optional settings with defaults: adapters, clients, servers, services, test builders.

```go
type Option func(*config)

func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

func NewClient(baseURL URL, opts ...Option) (*Client, error) {
    c := config{timeout: 5 * time.Second}
    for _, opt := range opts {
        opt(&c)
    }
    if c.timeout <= 0 {
        return nil, fmt.Errorf("client timeout %s: %w", c.timeout, ErrInvalidConfig)
    }
    return &Client{baseURL: baseURL, cfg: c}, nil
}
```

- Required values stay positional parameters, never options.
- Set defaults first, apply the options, then validate the result.
- Name options `With…`.
- Don't use options for domain values or commands: what the business requires must be explicit. A domain value with many optional parts usually hides a missing concept or a second state; model that instead.

## Review checklist

- [ ] No domain concept passed as a bare primitive; IDs of different aggregates are different types.
- [ ] Types with rules have unexported fields and a `Parse…`/`New…` constructor returning a wrapped domain error.
- [ ] Every type's zero value is either meaningful or detectably "not set".
- [ ] No money as float; value types are immutable and comparable.
- [ ] Parsing from primitives happens in adapters; no tags on domain types.
- [ ] Functional options only for optional settings; required values are positional; validated after applying.
