---
# Frontmatter fields: https://code.claude.com/docs/en/skills ("Frontmatter
# reference"). In plugin "gofast" this skill is gofast:grill. The drive
# loads it when the work is on the 'grill' stage: grill runs in the main
# session, live with the owner, never in a subagent. This file is the
# protocol only; the owner may replace its body with their own grilling
# skill, as long as the outputs below stay the same.
name: grill
description: The protocol of gofast's 'grill' stage, a live session with the owner that settles every open question before discovery. Loaded by /gofast:drive.
user-invocable: false
---

You run the **grill** stage of the work gofast is driving, **in this session, live with the owner**. Its approval is the owner's approval of the plan.

1. **Role.** Make sure everything about the work is clear before anyone looks at how it fits the code: intent, scope, behaviour, edge cases. You ask; the owner decides. Never guess an answer the owner hasn't given.

2. **Start.** Run `gf status --json`. Read the work description (`work.description`), the owner's inputs (`work.inputs`: the `spec`, if any; an input with `changed` set changed since the work started, so ask the owner about it), and `current.lastProblem`: the previous failure reason (a `grill-valid` failure lists what is missing) or the owner's rejection feedback, which this visit must address with another round. If the work has an event model (an `event-model` input), read the latest accepted event-modelling artifacts in `work.artifactDir`: `event-modelling-v<n>.md`, with its `## Assumptions` (`M1`, `M2`, …), and `event-model-v<n>.json`, with its slices and scenarios. If `current.returned` is set, the owner sent the work back to grill: its `reason` says what this visit must settle. If `current.reworkOf` is set, read the fixer's artifact (`current.reworkOf.fixerArtifact`) first.

3. **Rounds.** Ask in rounds. Each round asks every open question whose prerequisites are settled, numbered, each with your recommended answer, worded so that "yes" accepts it (e.g. "3. A debit larger than the balance is rejected with InsufficientFunds. Yes?"). Then end your turn and wait for the owner's answers; gofast lets the session stop once to wait. Never ask the next round before the owner answers this one. Cover:
   - **intent**: what the owner wants and why;
   - **scope**: what is in, and what is explicitly out;
   - with an event model, **every assumption** under its `## Assumptions`, by id, and its gaps: missing events, unclear rejections, edge cases;
   - **behaviour choices**: what happens on each edge case, each failure, each concurrent or repeated action;
   - anything a later stage (discovery, design, specify) would otherwise have to guess.

   Stop when no question is open. An answer that raises a new question opens it for the next round.

4. **Outputs.** Stay inside the stage's write scope: Only the artifacts in `work.artifactDir` and this work's decision document. Writes outside it are denied, and `gf submit` checks every file changed since the stage began, however it was written: a change outside the scope fails the attempt until you undo it. Never write the decisions directory's `README.md`: gf generates it. Write:
   - The **decision document**, `<work.decisionsDir>/<YYYY-MM-DD>-<work.id>-<slug>.md` (today's date, a short lowercase slug of the title, e.g. `docs/decisions/2026-10-12-w-1a2b3c4d-wallets.md`), with these sections: `## Intent`; `## Scope` (in / out); `## Decisions`, one per settled question, `D1`, `D2`, …, each with the question, the owner's answer and the rationale; `## Glossary`; `## Acceptance scenarios`, required when the work has **no** event model: `A1`, `A2`, …, each given / when / then in prose, together covering the agreed behaviour; and `## Open questions`, which says `None`. On a later visit, update the same document.
   - `grill-v<visit>.json`, next to `current.artifact` (e.g. `grill-v1.json`), the same content structured: `decisions` (`id`, `question`, `answer`, `rationale`, and `addresses`: the ids of the event model's assumptions the decision settles, e.g. `["M1"]`), `acceptanceScenarios` (`id`, `given`, `when`, `then`), `openQuestions` (empty) and `docPath` (the decision document's path from the repository root). With an event model, every assumption must be in some decision's `addresses`.
   - `current.artifact`: a short pointer to the decision document and a summary of the decisions.

5. **Finish.** Run `gf submit --passed` yourself, from this session, or `gf submit --failed "<reason>"` if the owner stops the grill. `gf submit --passed` runs these checks itself: `artifact-written`, `grill-valid`. `grill-valid` checks that the JSON parses, no question is open, decision ids are unique, the decision document is this work's and has its sections, acceptance scenarios exist without an event model, and every assumption is addressed. If one fails, the attempt fails; read `lastProblem` and the check log it names (with the Read tool), fix, and submit again. Then tell the owner which document to read (`work.grill.docPath`) and that approving it, with `gf approve` in their own terminal, approves the plan; `gf reject "<feedback>"` starts another round on the next `/gofast:drive`.

6. **Never** run `gf approve`, `gf reject`, `gf extend`, `gf rework-extend`, `gf stress`, `gf abandon`, `gf return` or `gf nfrs adopt`; never answer for the owner; never edit anything in `.gofast/` except grill's artifacts; never work on any stage but the current one.
