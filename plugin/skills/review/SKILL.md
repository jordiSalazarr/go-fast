---
name: review
description: How the owner reviews a Go change - first whether it does what was agreed, then correctness risks, then his conventions; a ranked risk map telling the owner which specific rules and behaviours in the change are dangerous to get wrong and how well they are protected; only verified findings with evidence; blocking vs should-fix vs note, and a verdict that fails only on blocking findings. Use when reviewing a change, a diff, a pull request or a stage's code, and in the gofast review stage.
---

# Review

A review does two jobs. It finds what would make the owner reject this change, and proves it. And it tells the owner **where this change is dangerous**: the specific rules and behaviours that would hurt most if they were wrong, so he knows where to spend his own attention even when the review passes. It is judged by whether its blocking findings are real, its verdict is right and its risk map points at the right places, not by how many comments it makes. `gf` has already built, formatted and run the tests; don't spend the review on what a tool checks.

## What to read first

1. What was agreed: the grill decision document (intent, scope, decisions, scenarios, `## Must not change`), the design (aggregates, invariants, contracts), the plan on a refactor, and, on a rework visit, the fixer's artifact.
2. The whole diff since the work started, then enough surrounding code to understand each change. Read the tests as carefully as the code.

## What to look for, in this order

1. **Does it do what was agreed?** Map each agreed scenario, decision and invariant to the code and the test that proves it. Something missing, different or added beyond scope is a finding. On a refactor, anything under "must not change" that changed is blocking.
2. **Correctness.** Races and lost updates, repeated commands and events (idempotency), error paths that swallow or mis-map errors, edge cases (empty, zero, boundaries, time zones), partial failure (a side effect before commit, two writes without a transaction), resource leaks, and security (input trusted too early, secrets in logs).
3. **Conventions.** Apply aggregate-design, functional-domain, value-types, code-layout, schema-evolution and reads-and-reactions, using their review checklists.
4. **Tests.** Do they pin behaviour or just execute code? Look for assertions that can't fail, tests coupled to internals, rejections with no test, and mocks in domain tests.
5. **Simplicity.** Dead code, speculative abstraction, an interface with no consumer, a generalisation nobody asked for.

## Risk map: where the perils are

While reading, keep track of the places where a mistake would cost the most. This is not a list of defects. It is a map for the owner, written whatever the verdict.

- Pick the **3 to 5 riskiest points** of this change, ranked. Fewer is fine for a small change. Never pad the list, and never list every file touched.
- A point is risky when a mistake there would be **costly** (money, data loss or corruption, a broken published contract, a security hole, a stuck process) and **easy to get wrong or hard to notice** (concurrency, retries and duplicates, ordering, state transitions, time and time zones, rounding and units, error mapping, migrations, anything only exercised in production).
- For each point, name the exact rule or behaviour at stake in business terms ("a transaction is applied to a wallet at most once"), not just a file.
- Say how well it is protected: which test pins it, or that nothing does; and whether a test can pin it at all (a race may need a stress run or a manual check).
- Say what the owner should check himself, in one sentence: a scenario to try, a line to read, a question for the domain expert.
- A risk point with no protection that you can turn into a concrete failure scenario is also a blocking finding. Cross-reference it.

## Severity

**Blocking** (the verdict fails):
- behaviour differs from what was agreed, an agreed scenario is missing, or scope crept;
- a correctness bug, or a plausible one with a concrete failure scenario;
- a business rule outside the domain, an aggregate shaped like a table, a domain concept passed as a primitive in the domain API, a slice importing a slice, a side effect before commit, a write decision from a lagging read model, a non-idempotent reaction;
- a published contract broken without a new version;
- an agreed scenario or a rejection with no test that pins it;
- a security issue.

**Should fix**: a convention slip that won't spread (a weak name, a small missing test, a needless export).

**Note**: a preference, or a question worth asking.

Fail the review if and only if there is at least one blocking finding. Should-fix items and notes never fail it; they go to the owner in the artifact.

## Every finding is verified

- Before writing a finding, re-read the code and confirm it. For a correctness finding, write the concrete scenario: the inputs or interleaving, and the wrong result. If you can show it with a test command, do.
- If you can't build a concrete scenario, it is not blocking: write it as a note or a question.
- State the rule or agreement it breaks (the skill and rule, or the scenario ID), and the direction of the fix in a sentence. Don't write the fix; you can't change code.

## What not to do

- Don't re-open what the owner approved. If an approved decision now looks wrong, put it under `## Decisions for the owner`. It does not block.
- Don't demand changes outside the work's diff, unless the diff breaks them.
- Don't flag formatting, import order or anything a linter decides.
- On a rework visit, check that the fixer's issue is resolved and nothing regressed. New findings are fine; repeating last visit's resolved ones is not.

## Stress decision

Where the protocol asks for one, include stress testing when the change touches concurrency or locking, I/O or queries, loops over data that can grow, serialization of large payloads, caching, or a hot path named by an NFR. Skip it for changes that can't affect behaviour under load. The reason names what in the diff led to the choice.

## Artifact

```markdown
## Verdict
Passed | Failed: <n> blocking

## Risk map
| # | Rule or behaviour at stake | Where | Why risky | Protected by | Check yourself |
|---|---|---|---|---|---|
| R1 | a transaction applies to a wallet at most once | slices/postTransaction/postTransaction.go:61 | retried requests after a timeout would double-charge | postTransaction_test.go:120 (sequential only) | send the same transaction twice concurrently |

## Agreed behaviour
| Item | Implemented in | Proven by | OK |
|---|---|---|---|
| A1 frozen wallet rejects posting | domain/wallet.go:42 | postTransaction_test.go:88 | ✓ |

## Blocking
### B1 <one-line title>
- Where: path/file.go:123
- Breaks: <skill: rule> or <scenario ID>
- Scenario: <inputs or interleaving → wrong result>
- Fix direction: <one sentence>

## Should fix
- S1 path/file.go:45: <what and why>

## Notes
- N1 …

## Decisions for the owner
<only if something approved now looks wrong: options, trade-offs, recommendation>

## Stress decision
include | skip: <what in the diff led to it>
```

Omit empty sections, except Verdict, Risk map and Agreed behaviour.
