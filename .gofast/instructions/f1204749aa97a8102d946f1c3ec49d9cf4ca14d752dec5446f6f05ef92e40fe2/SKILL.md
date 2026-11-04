---
name: discovery
description: How the owner investigates before changing anything - name what is being discovered first, start from the slice that owns the behaviour and follow its upstream and downstream slices through events and contracts, and for errors, incidents or slowness gather evidence beyond code from logs, traces, metrics and alarms (Logfire or any connected observability tool), read-only; every finding backed by evidence, facts kept apart from hypotheses. Use when investigating a bug, error, incident or slow operation, working out where a new or changed behaviour fits, mapping what a change affects, and in the gofast discovery stage.
---

# Discovery

Discovery establishes **what is true now**, with evidence, so the next stages build on facts. Name the question first, start from the slice that owns the behaviour, follow its neighbours, and when something went wrong, look at what actually happened in the running system, not only at what the code says could happen. Discovery describes; it doesn't design the fix or change code.

## 1. Name what you are discovering

Write the question as one sentence at the top of the artifact before exploring. Every finding must help answer it, and you stop when it's answered.

| Kind | The question | Typical source |
|---|---|---|
| Behaviour to add or change | Where does the agreed behaviour live today, and what must change? | new-feature, modify-behaviour |
| Something went wrong | What happened, to whom, since when, and why? | fix-bug, an error, an incident |
| Code to move | What is this code, who depends on it, and how well is it tested? | refactor |
| Something is slow | Which operation, where does its time go, against which target? | improve-performance |

## 2. Start from the slice, then its neighbours

A slice is the unit of behaviour, so the behaviour in question almost always belongs to one or a few slices.

1. **Find the owning slice(s)**: the command slice, query slice or reaction that does it. Read its application logic, the domain it calls (aggregate, states, invariants, rejections), its adapters and its tests. The tests describe what the code is meant to do today.
2. **Follow the neighbours through events and contracts**, not imports (slices never import each other):
   - **Upstream**: what triggers it. The HTTP or CLI entry point, reactions that issue its command, events it consumes, and facts it loads from other aggregates or read models.
   - **Downstream**: who uses what it produces. Reactions on its events, read models built from them, published events and their subscribers, the outbox relay.
   - **Shared**: bounded-context adapters and read models it shares with other slices, and the other commands of the same aggregate, which share its invariants.
3. **Find them by name**: search the repo for the event type names, command names and contract names the slice uses, plus the router and wherever reactions are registered.
4. **Classify each neighbour** with a reason: it could **cause** the behaviour, it would be **affected** by changing it, or it is **unaffected**.

## 3. Beyond code: what actually happened

For errors, incidents and slowness, the code says what can happen; telemetry says what did. Look at the running system before reasoning from code.

- **Check what you can reach**: observability tools connected to the session (a Logfire, Grafana, Datadog, Sentry, CloudWatch or Prometheus MCP server, or their CLIs) and read-only access to logs or data. Use them.
- **Work in this order:**
  1. **Scope in time and reach**: when it started, how often, which users, tenants or operations. Compare the start with deploys and commits from that window (`git log --since`).
  2. **Alerts and metrics** around that window: error rate, latency percentiles, saturation, queue depth, retries.
  3. **Traces**: follow one failing request end to end, and compare it with a successful one of the same operation. Note where they diverge.
  4. **Logs and exceptions** for those trace or correlation IDs: the full wrapped error chain.
  5. **Data**, read-only: the stored state or event log for an affected entity (duplicated transactions, a state that should be impossible).
  6. **Back to code**: map the span, log line or error to its slice and line.
- **Read-only, always.** Never change configuration, acknowledge or silence alerts, replay messages, or write to any system. Keep queries narrow: a time window and a limit.
- **Artifacts are committed.** Don't copy secrets, tokens or personal data into them; reference trace IDs and links, and redact payloads.
- **If no tool is connected**, say so in the artifact, list the evidence the owner could fetch (which query, which dashboard, which time window), and continue from code and reproduction.
- **Reproduce** the problem the smallest way you can: a test command, or a request against a local instance. Record the exact steps and output. If it doesn't reproduce, say what you tried.

## 4. Facts apart from hypotheses

- A **fact** is a claim plus its evidence: `file:line`, a trace ID or link, a redacted log excerpt, a metric value and its window, command output.
- A **hypothesis** is labelled as one, ranked by the evidence for it, with what would confirm or refute it.
- Don't propose the fix. When what was agreed and what the code does disagree, or the evidence allows two readings that change behaviour, put it under `## Decisions for the owner` with the options and a recommendation.

## Before grill: describe, don't decide

On feature paths and refactors, discovery runs **before** grill and feeds it. There is no agreement yet, so scope the exploration by the work's inputs (event model, spec, or description) and the slices they touch.
- Describe what exists; never propose a solution or pick a behaviour. Proposals anchor the interview on today's code.
- End with `## Questions for grill`: each conflict between the input and the code, and each unknown that only the owner can settle, with the evidence. Grill puts them on its first round.

## What each kind must contain

- **Behaviour**: the affected slices (new, changed, unchanged), the aggregates and invariants the change will touch, the contracts involved (internal or external), and conflicts between the input and the code. On modify-behaviour, `## Current behaviour` with evidence, and `## Expected behaviour` as the input asks for it (grill settles it).
- **Something went wrong**: symptom, reach, timeline, reproduction, the evidence chain from symptom to code, ranked causes, and the slices that are causes or affected.
- **Code to move**: the code, its dependents, its tests, and coverage per package.
- **Slow**: the operation, its path through slices and adapters, where its time goes (traces or profiles), and current numbers against the NFR.

## Artifact

```markdown
## Question
<one sentence>

## Answer
<three to five lines>

## Slice map
| Slice | Role | Via | Cause / affected | Evidence |
|---|---|---|---|---|
| postTransaction | owner | — | cause | slices/postTransaction/postTransaction.go:58 |
| awardPointsOnTransactionPosted | downstream | TransactionPosted | affected | reaction registered in cmd/server/main.go:40 |

## Evidence
| # | Fact | Evidence |
|---|---|---|
| E1 | duplicate postings began after the 14:05 deploy | Logfire trace 7f3a…, deploy a1b2c3 at 14:02 |

## Hypotheses
1. <hypothesis> (supported by E1, E3). Confirm by: <what>

## Reproduction
<exact steps and output, or what was tried>

## Not checked
<tools not connected, evidence the owner could fetch>

## Questions for grill
<feature and refactor paths: conflicts and unknowns for the owner, each with its evidence>

## Decisions for the owner
<fix-bug and performance paths, only when needed>
```

Omit empty sections, except Question, Answer and Evidence.
