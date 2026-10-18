---
# Frontmatter fields: https://code.claude.com/docs/en/skills ("Frontmatter
# reference"). In plugin "gofast" this skill is gofast:grill. The drive
# loads it when the work is on the 'grill' stage: grill runs in the main
# session, live with the owner, never in a subagent. The interview method is
# the owner's: gofast:grilling and gofast:domain-modeling, vendored unchanged
# from mattpocock/skills (MIT; see their SOURCE.md). This file adds what
# gofast needs and the outputs grill-valid checks; keep the outputs as they are.
name: grill
description: The protocol of gofast's 'grill' stage, a live session with the owner that settles every open question, after discovery. Loaded by /gofast:drive.
user-invocable: false
---

You run the **grill** stage of the work gofast is driving, **in this session, live with the owner**. Its approval is the owner's approval of the plan.

1. **Role.** Make sure everything about the work is settled before anyone designs or writes code: intent, scope, behaviour, edge cases, the domain language. You ask; the owner decides. Never guess an answer the owner hasn't given.

2. **Start.** Run `gf status --json`. Read the work description (`work.description`), the owner's inputs (`work.inputs`: the `spec`, if any; an input with `changed` set changed since the work started, so ask the owner about it), and `current.lastProblem`: the previous failure reason (a `grill-valid` failure lists what is missing) or the owner's rejection feedback, which this visit must address with another round. If the work has an event model (an `event-model` input), read the latest accepted event-modelling artifacts in `work.artifactDir`: `event-modelling-v<n>.md`, with its `## Assumptions` (`M1`, `M2`, …), and `event-model-v<n>.json`, with its slices and scenarios. **Read discovery's accepted artifact in `work.artifactDir`**: its slice map, its evidence, the current behaviour and its `## Questions for grill` are your starting facts; don't re-explore what it established. The current behaviour is a fact, not a decision: when an input asks for something different from what the code does, the question is "change it to X?", never "keep it?". If `current.returned` is set, the owner sent the work back to grill: its `reason` says what this visit must settle. If `current.reworkOf` is set, read the fixer's artifact (`current.reworkOf.fixerArtifact`) first.

3. **Rounds.** Load the `gofast:grilling` and `gofast:domain-modeling` skills with the Skill tool and follow them for the whole session: rounds over the frontier of the design tree, each question numbered with your recommended answer worded so that "yes" accepts it, facts looked up rather than asked, terms challenged and written to the glossary as they are resolved, ADRs only when domain-modeling's three conditions hold. After each round, end your turn and wait for the owner's answers; gofast lets the session stop once to wait. Discovery's `## Questions for grill` go on the first round. The design tree must cover:
   - **intent**: what the owner wants and why;
   - **scope**: what is in, and what is explicitly out;
   - with an event model, **every assumption** under its `## Assumptions`, by id, and its gaps: missing events, unclear rejections, edge cases;
   - **behaviour choices**: what happens on each edge case, each failure, each concurrent or repeated action;
   - anything a later stage (design, specify) would otherwise have to guess.

   On a **refactor** (`work.type` is `refactor`), cover instead:
   - **intent**: why the refactor, and what it should make easier;
   - **scope**: what is in, and what is explicitly out;
   - **what must not change**: public APIs, behaviour, event names, wire formats; a refactor always preserves something;
   - **the order of steps**, and anything the plan would otherwise have to guess.

   Without an event model on feature work, show the acceptance scenarios you wrote from the answers in the last round, so the owner sees every scenario specify will cover. Stop when the frontier is empty and the owner confirms the shared understanding.

4. **Outputs.** Stay inside the stage's write scope: the artifacts in `work.artifactDir`, this work's decision document, the glossaries (`GLOSSARY.md`, `GLOSSARY-MAP.md` and the context glossaries it points to) and ADRs under a `docs/adr/` directory. Writes outside it are denied, and `gf submit` checks every file changed since the stage began, however it was written: a change outside the scope fails the attempt until you undo it. Never write the decisions directory's `README.md`: gf generates it. Write:
   - The **decision document**, `<work.decisionsDir>/<YYYY-MM-DD>-<work.id>-<slug>.md` (today's date, a short lowercase slug of the title, e.g. `docs/decisions/2026-10-12-w-1a2b3c4d-wallets.md`), with these sections: `## Intent`; `## Scope` (in / out); `## Decisions`, one per settled question, `D1`, `D2`, …, each with the question, the owner's answer and the rationale, and a link to its ADR if it earned one; `## Glossary`, the terms this work introduced or sharpened; `## Acceptance scenarios`, required when the work has **no** event model: `A1`, `A2`, …, each given / when / then in prose, together covering the agreed behaviour; `## Must not change`, required on a **refactor**: a list (`- …`), one item per thing the refactor preserves, never empty and never just `None`; and `## Open questions`, which says `None`. A refactor changes no behaviour, so it needs no acceptance scenarios. Open each decision and each acceptance scenario with its id at the start of a line, as a bullet `- D1: …` / `- A1: …` or as a heading `### D1` (optionally followed by `:` or ` —`); an id elsewhere in a line doesn't count. With an event model, name in each decision's entry the assumptions it settles, by id (e.g. "settles M1"). On a later visit, update the same document.
   - `grill-v<visit>.json`, next to `current.artifact` (e.g. `grill-v1.json`), the same content structured: `decisions` (`id`, `question`, `answer`, `rationale`, and `addresses`: the ids of the event model's assumptions the decision settles, e.g. `["M1"]`), `acceptanceScenarios` (`id`, `given`, `when`, `then`), `mustNotChange` (on a refactor: the items of `## Must not change`, the same number, in the same order), `openQuestions` (empty) and `docPath` (the decision document's path from the repository root). With an event model, every assumption must be in some decision's `addresses`. **The document and the JSON must list the same decision ids and the same acceptance scenario ids**: the owner approves the document, and later stages read the JSON.
   - `current.artifact`: a short pointer to the decision document and a summary of the decisions.

5. **Finish.** Run `gf submit --passed` yourself, from this session, or `gf submit --failed "<reason>"` if the owner stops the grill. `gf submit --passed` runs these checks itself: `artifact-written`, `grill-valid`. `grill-valid` checks that the JSON parses, no question is open, decision ids are unique, the decision document is this work's and has its sections, acceptance scenarios exist on feature work without an event model, a refactor's `## Must not change` is there and agrees with `mustNotChange`, every assumption is addressed, and the document lists the same ids as the JSON, naming each assumption in the entry of the decision that addresses it. If one fails, the attempt fails; read `lastProblem` and the check log it names (with the Read tool), fix, and submit again. Then tell the owner which document to read (`work.grill.docPath`) and that approving it, with `gf approve` in their own terminal, approves the plan (on a refactor: the intent and what must not change; the plan comes next); `gf reject "<feedback>"` starts another round on the next `/gofast:drive`.

6. **Never** run `gf approve`, `gf reject`, `gf extend`, `gf rework-extend`, `gf stress`, `gf abandon`, `gf return` or `gf nfrs adopt`; never answer for the owner; never edit anything in `.gofast/` except grill's artifacts; never work on any stage but the current one.
