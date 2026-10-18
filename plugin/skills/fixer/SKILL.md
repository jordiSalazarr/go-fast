---
name: fixer
description: How the owner finds the root cause of a failed review, integration or stress run and sends the work back to the right stage - reproduce the failure, trace it from symptom to the stage where the mistake was made, route to that origin (not the latest convenient stage), and leave the next visit precise instructions; in performance mode, diagnose the bottleneck from measurements first. Use when a change failed review, integration tests or stress tests, when deciding why something broke and where to fix it, and in the gofast fixer stage.
---

# Fixer

Find the root cause, prove it, and send the work to the **stage where the mistake was made**. Patching a symptom in a later stage spends a rework (a work gets three) and brings the same failure back. A fixer is judged by whether its route fixes the failure on the next pass, and nothing else breaks.

## 1. Read the failure

- The failing stage's artifact, the check log it names, and `lastProblem`: the exact check, test, finding or measurement that failed.
- The earlier artifacts of this work: the agreement, design, specs, plan or baseline the code was built from.
- The diff since the work started.
- **Earlier fixer artifacts of this work.** If this is the same failure again, the previous route was wrong. Go earlier, and say so.

## 2. Reproduce it

- Re-run what failed: the failing test (`go test -run '^TestX$' ./pkg`), the integration command, or read the k6 output and measurements. You can run commands; you can't change code or tests.
- If it failed in a running environment, use the connected observability tools as discovery does: traces, logs and metrics, read-only.
- **Flaky is a claim that needs evidence.** Run it several times and show the results. A test that flakes is itself a defect: in the test (route to specify) or in the code, for example a race (route to implement). Never route "just retry".

## 3. Trace symptom → cause → origin

Ask "why" until you reach a decision some stage made:

> **Symptom**: integration shows a transaction applied twice.
> **Cause**: `postTransaction` doesn't check already-applied transaction IDs.
> **Origin**: the design gave `PostTransaction` no idempotency choice, and no spec covers a repeated command.

Back every link with evidence (`file:line`, test output, trace ID). The origin is the earliest stage whose artifact is wrong or missing something. The route goes there.

## 4. Choose the route

| The mistake is in | Signs | Route |
|---|---|---|
| The event model | missing event or scenario, wrong command or flow | event-modelling (if the path ran it) |
| The model | missing invariant, rejection, slice or idempotency choice; wrong aggregate boundary; contract wrong | design |
| The specs | the failing case has no spec; a spec asserts the wrong behaviour or passes for the wrong reason | specify |
| The code | the code doesn't do what the design and specs say; a review finding on conventions | implement |
| The refactor plan | wrong moves, areas too narrow | plan |
| The pinned behaviour | a must-not-change behaviour isn't pinned by a characterization test | characterize |
| The measurement | baseline, script or diagnosis wrong | measure |
| The optimization | targets not met though measurement and diagnosis hold | optimize |

Rules for choosing:
- **Behaviour no spec covers goes to specify, not implement**, even when the code fix is one line. The next visit then pins it with a failing spec before the code changes.
- **A fix that would contradict an earlier artifact goes to that artifact's stage.** If the code needs a rejection the design doesn't have, route to design; otherwise implement and design disagree and review fails again.
- **Otherwise route to the latest stage that can fix it**, so the least work reruns.
- Only stages allowed on this path are valid (gf rejects the rest), and grill is never a route. If the agreement itself is wrong or ambiguous, put it under `## Decisions for the owner`, recommend `gf return grill "<reason>"`, and route to the earliest allowed stage.
- In the artifact, say why you didn't choose the alternative routes.

## 5. Performance mode

When stress testing failed:
1. Write `## Bottleneck` first, from measurements, not intuition: which NFR target failed and by how much, and where the time goes (profiles, traces, query plans, lock contention), with evidence. This unlocks the catalog skills; load the ones that match the bottleneck.
2. Then route:
   - a wrong script, baseline or diagnosis → measure;
   - code that is too slow → optimize, or implement on paths without optimize;
   - a model that forces contention (an aggregate every request writes to, a read served by loading aggregates) → design.

## 6. Instructions for the next visit

The next visit reads your artifact first. Leave nothing to guess:
- **What is wrong**, in one sentence, with the evidence.
- **What must change**: the decision to revisit (the design's missing choice, the spec to add, the code behaviour to correct), not the code itself.
- **How it will be verified**: which spec, check or measurement must pass.
- **What must not change**: approved decisions and behaviour that are fine, so the next visit doesn't widen scope.

## Artifact

```markdown
## Failure
<stage, check or finding, the exact failing output (short)>

## Reproduction
<command and result; for flakiness, the repeated runs>

## Root cause
- Symptom: … (evidence)
- Cause: … (evidence)
- Origin: <stage> — … (evidence)

## Bottleneck
<performance mode only: target vs measured, where the time goes, evidence>

## Route
<target>: <why>. Not <alternative>: <why not>.

## For the next visit
- Wrong:
- Change:
- Verify by:
- Keep:

## Repeated failure
<only if this failure was routed before: what the previous route missed>

## Decisions for the owner
<only when the agreement itself is in question>
```
