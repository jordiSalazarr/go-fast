# Design: skill improvements flow back to the plugin (w-7ec48ee9, visit 1, attempt 1)

First attempt; nothing changed since an earlier one. Structured form: `design-v1.json` next to this file. No event model was given (event-modelling skipped), so this design holds the events, commands, read models and slices too. It follows grill's decisions D1–D23 and ADR 0004. Where it adds something grill did not settle, the addition is listed under `## Decisions for the owner`.

## Bounded context

**skill-improvements**. It has two halves joined by one GET and by the history sync:

- **historyserver**: members write, revise, approve and withdraw a *Skill improvement*. The server serves a project's approved improvements to the project secret and shows members where each one landed.
- **workflow (gf + plugin)**: at startup, gf fetches what is approved, works out what the loaded plugin directory has not recorded yet, and briefs the session. The improvement agent edits one skill at a time. gf records each outcome in the skill's `.applied-improvements` file and in the repository's history.

Data flows server → gf only through a GET that gf starts (ADR 0004). gf → server stays the sync it already does. Assessments are unchanged and still never approve, reject or route a work.

## Aggregate 1: `SkillImprovement` (historyserver)

One member-written proposal to change one skill's wording.

**Invariants**
- S1. An improvement names one skill and an instruction version of that skill its project has recorded.
- S2. An improvement's text changes only while it is proposed.
- S3. Only a proposed improvement is approved, once, for its project or for every project of its organisation.
- S4. An approval approves the text its approver read: the improvement's latest revision.
- S5. Only an approved improvement is withdrawn, and a withdrawn improvement is never approved again.

**States** (one type each, sealed interface `SkillImprovement`):
- `NoImprovement`: the initial state.
- `ProposedImprovement`: id, project, skill, improves (version), text, revision.
- `ApprovedImprovement`: adds scope, approvedBy, approvedAt.
- `WithdrawnImprovement`: adds withdrawnBy, withdrawnAt.

Only `ProposedImprovement` has `Revise` and `Approve`, and only `ApprovedImprovement` has `Withdraw`. The domain function for each command switches on the state and returns the rule's rejection for the other states.

**State the rules read**: skill, improves, text, revision, scope, the state itself. The author, the originating assessment (work, stage, visit) and the approver's and withdrawer's names are carried on the events for the viewer. No rule reads them.

**Value types**: `SkillImprovementID` (server-made, `si-<hex>`, parsed), `SkillName` (parsed: a skill directory name), `ImprovementText` (trimmed, 1–4000 characters, like an assessment's reason), `Revision` (≥ 1), `ApprovalScope` (`ThisProject` | `AllProjects`; the zero value is "not set" and is rejected at the edge), `InstructionVersion` (the existing `ContentHash`). `RecordedVersionFact` is a fact the application layer loads from the project's synced `SkillLoaded`/`AgentStarted` instructions (audit.go already lists them) and passes in.

| Command | State it runs on | Events | Rejection → invariant |
|---|---|---|---|
| `ProposeSkillImprovement(id, project, skill, improves, text, from?, RecordedVersionFact, at)` | `NoImprovement` | `SkillImprovementProposed` (revision 1) | `UnknownInstructionVersion` → S1 |
| `ReviseSkillImprovement(text, at)` | `Proposed` | `SkillImprovementRevised` (revision + 1) | `ImprovementTextFixed` → S2 (on Approved or Withdrawn) |
| `ApproveSkillImprovement(scope, readRevision, member, at)` | `Proposed` | `SkillImprovementApproved` | `ImprovementAlreadyApproved` → S3 (Approved with another scope); `ImprovementWithdrawn` → S3; `ImprovementRevisedSinceRead` → S4 (readRevision ≠ revision) |
| `WithdrawSkillImprovement(member, at)` | `Approved` | `SkillImprovementWithdrawn` | `ImprovementNotApproved` → S5 (on Proposed) |

Any member may approve, the author included (D3). The `inspects` middleware already restricts every member call to the project's organisation. That is authorisation, not a domain rule. "All projects" means every current and future project of the improvement's project's organisation (D21). Members only reach their own organisation's projects, so this is the approving member's organisation. Withdrawing an all-projects approval withdraws it everywhere, because it is one aggregate (D21).

**Storage, concurrency, idempotency**
- The server keeps its append-only style (assessments, events): a new `skill_improvement_events` table (seq, project, improvement, version, type, payload, member, at), with `UNIQUE (improvement, version)` and append-only triggers. State is the fold of `Evolve` over an improvement's rows.
- **Optimistic concurrency**: load with version → decide → insert at version + 1. On a unique-constraint conflict, re-run up to 3 times, then 409 `conflict`. Revise racing approve is the case that matters: S4 makes the loser fail visibly instead of approving text nobody read.
- **Idempotency**:
  - Approve repeated with the same scope on an `ApprovedImprovement`: no events, 200.
  - Withdraw repeated on a `WithdrawnImprovement`: no events, 200.
  - Revise with the current text: no events.
  - Propose takes a client-made id from the viewer (the form generates it once), so a retried POST lands on the same improvement and returns it with no new event.

## Aggregate 2: `PluginSkill` (workflow)

One gofast skill directory in the plugin directory the session loaded, `$CLAUDE_PLUGIN_ROOT/skills/<name>/`. Its state is the skill's **record**, `.applied-improvements` (D14), plus the skill's current instruction version. The record is a dotfile, so it is already outside the version hash (instructions.go skips dotfiles).

**Invariants**
- P1. An improvement is recorded at most once in a skill's record, until the owner deletes its line.
- P2. A skill has at most one improvement in progress, held by one session.
- P3. An outcome is recorded only for the improvement in progress on that skill, by the session holding it.
- P4. Only gofast's own skills of the loaded plugin directory are improved, never a vendored skill.
- P5. An applied improvement that changed the skill counts as declined once the skill is back at a version it had before that improvement.

**States**: `IdleSkill` (no line in progress) and `ImprovingSkill` (one `in-progress` line, its session and its before-version). Each record line is an entry: `<improvementId> <outcome> <before> <after|-> [<session>] [# reason]`, with outcomes `in-progress`, `applied`, `not-applied` and `declined`. The owner may delete any line (D15, D23). gf treats a line it cannot parse as "recorded", so a hand-edited line never causes a second application, and reports it in the brief.

**Value types**: `SkillDir` (absolute, under `$CLAUDE_PLUGIN_ROOT/skills/`, parsed), `SessionID` (from the SessionStart input's `session_id`, exported by the hook as `GOFAST_SESSION` through `CLAUDE_ENV_FILE`, next to the existing `GOFAST_ACTOR`), `ApprovedImprovementFact` (id, skill, text, approval order; from the fetched list), `VendoredSkill` (a skill directory holding `SOURCE.md`, which grilling and domain-modeling have).

| Command | State it runs on | Events | Rejection → invariant |
|---|---|---|---|
| `StartSkillImprovement(improvement ApprovedImprovementFact, session, currentVersion, vendored bool)` | `Idle` | `SkillImprovementStarted` (before = currentVersion) | `ImprovementAlreadyRecorded` → P1; `SkillImprovementInProgress` → P2 (another session's line, or another id); `SkillOutOfScope` → P4 |
| `ImproveSkill(id, session, afterVersion, summary?)` | `Improving` | `SkillImproved` (before, after; after = before when the skill already held it, A8) | `ImprovementNotInProgress` → P3 |
| `ReportImprovementNotApplied(id, session, cause conflict\|refused, reason)` | `Improving` | `SkillImprovementNotApplied` | `ImprovementNotInProgress` → P3 |
| `DeclineSkillImprovement(currentVersion)` | any | zero or more `SkillImprovementDeclined`, one per applied entry P5 now holds for | none: it only notices a fact |

`DeclineSkillImprovement` rule (P5): an `applied` entry E with before ≠ after counts as declined when the current version equals E's before-version, or the before-version of an applied entry recorded before E. Reverting the latest improvement declines only that one. Reverting to before the first declines all of them. A no-change application (A8) is never declined. The entry's outcome becomes `declined`, so the decline is recorded once. Deleting the line re-applies it (D15).

**Storage, concurrency, idempotency**
- The record file is the source of truth for P1–P3, because it travels with the plugin directory (D14, A12). Each command runs load → decide → write under an **exclusive advisory lock on the record file** (pessimistic). The reason: two repositories sharing one plugin directory can start sessions at the same moment (A12), the file has no version to compare, and the lock is held for milliseconds, not for the agent's edit. The in-progress line is what keeps the two sessions apart for the whole edit (P2).
- After the record is written, gf appends the event to the repository's new **`skill-improvements` stream** (no work; it exists even with no active work, D18), through the existing optimistic event-log session. It then runs the same best-effort sync `gf approve` runs. The record is written before the history because a crash between the two must never cause a second application. The cost is a possible missing history entry, logged to `.gofast/gf.log`. See Decisions.
- **Idempotency**:
  - Start repeated by the session that holds the line: no events.
  - `ImproveSkill` or `ReportImprovementNotApplied` repeated by the holding session after its outcome is recorded with the same outcome: no events. A different outcome is refused by P3, because nothing is in progress.
  - `DeclineSkillImprovement` is naturally idempotent: declined entries are no longer `applied`.

## Where each piece runs (slices)

| Slice | Kind | Change | What it does |
|---|---|---|---|
| `ProposeSkillImprovement` | command | added | Viewer form, from a skill's instruction page or a went-wrong assessment (A1). `POST …/skill-improvements` with a member token. |
| `ReviseSkillImprovement` | command | added | `POST …/{id}/revise` (a named intent, never `PUT` on the improvement). |
| `ApproveSkillImprovement` | command | added | `POST …/{id}/approve` `{scope, revision}` (A2, A3). |
| `WithdrawSkillImprovement` | command | added | `POST …/{id}/withdraw` (A4). |
| `ApprovedSkillImprovements` | query | added | `GET …/skill-improvements/approved`, **project secret only** (D7, A15). |
| `SkillImprovementsOfOrganisation` | query | added | Viewer list and detail with landings (D20, A12, A17), member token. |
| `BriefAgentOnSessionStart` | query | changed | Runs only when `source == "startup"` (D8, A14). With sync configured, it runs `DeclineSkillImprovement` per skill, GETs the approved list (≤ 3 s timeout), computes the pending ones and briefs the session to run `gofast:improve-skills` before anything else (D18). |
| `StartSkillImprovement` | command | added | `gf improvement start <id>`, run by the improvement agent. It prints the skill directory and the approved text. |
| `ImproveSkill` | command | added | `gf improvement applied <id> [--summary]`. gf computes the after-version itself. |
| `ReportImprovementNotApplied` | command | added | `gf improvement not-applied <id> --conflict\|--refused "<reason>"` (A9, A10). |
| `DeclineSkillImprovement` | command | added | Run by the startup hook, not by an agent. |

Also changed, not separate slices:
- **`guardAgentActions`**: a new agent, `gofast:improve-skills`, gets a write scope of exactly the directory of the skill whose record holds its session's `in-progress` line, minus the record itself (only gf writes it). Its Bash is limited to `gf improvement …`. Every other path or command is denied (D19). The guard's rule is a pure domain function next to the existing scope rules, fed the record and the path. Outside an in-progress improvement, the agent's scope is empty.
- **`observeSkillLoads`**: `SkillLoaded` gets the optional `improvements` field (see Contracts, and Decision 1). The loaded version is already re-read on every load, so A16 holds as it is.
- **`syncHistory`**: unchanged in code. It sends every stream of the log, so `skill-improvements` syncs like the others. The server accepts any stream name.
- **Plugin**: a new agent definition `plugin/agents/improve-skills.md`, and a line in `hooks.json` is not needed (the hook already fires on startup, and the source filter is in gf).

### The startup flow (A5, A11, A13)
1. The hook does nothing more when sync is not configured (A13).
2. For each gofast skill with a record, it runs `DeclineSkillImprovement` against the current version.
3. It GETs `…/skill-improvements/approved` with the project secret.
   - **Unreachable or refused**: one line in the brief ("skill improvements: the history server could not be reached; nothing was applied"), and the session goes on (D8).
4. **Pending** = approved improvements whose id is not in their skill's record and whose skill exists in the plugin directory and is not vendored. Improvements for missing or vendored skills are skipped silently, and they stay pending for a plugin directory that has the skill. Pending improvements are grouped by skill, with skills ordered by their earliest approval and improvements within a skill in approval order (D17).
5. The hook writes the fetched list to `.gofast/skill-improvements.json`, a local file, gitignored and not synced. `gf improvement start` takes the `ApprovedImprovementFact` from that file, so the agent works on exactly what was fetched and gf makes no second GET.
6. The brief tells the session to run `gofast:improve-skills` now, without asking. It also lists any `in-progress` line left by an interrupted session, and how to retry it (Decision 3).
7. The agent works on each improvement in turn:
   1. `start`;
   2. edit by intent (D16);
   3. `applied`, or `not-applied` for a conflict or a refusal.
8. The agent ends with one summary and `git diff` of `$CLAUDE_PLUGIN_ROOT` for the owner (D6, D17). Nothing is committed.

A withdrawal between the fetch and `start` is not seen until the next startup. See Decision 6.

## Read models

| Read model | Question | Fields | How it is kept | Consistency |
|---|---|---|---|---|
| `ApprovedSkillImprovements` | Which improvements may this project's clones apply? | id, skill, improves, text, revision, scope, approvedAt, in approval order | Row 1: a query over `skill_improvement_events` of improvements approved for this project, or for all projects of its organisation, and not withdrawn. | immediate |
| `SkillImprovementsOfOrganisation` | What was proposed and approved, and where did each land? | per improvement: state, text, author, from-assessment, scope, approver, withdrawer; landings: project, owner (the envelope actor), outcome (applied, declined, not-applied with reason), before, after, at | Row 1: a query over `skill_improvement_events` plus the synced `skill-improvements` streams of the organisation's projects (the events table). | immediate for server-side facts; landings lag until each clone syncs |
| `PendingSkillImprovements` | What should this session apply now? | per skill, in order: id, text | Computed at startup: the approved list from the server minus the ids in each skill's record. The record is the source of truth, the stream only mirrors it. | immediate (read at startup) |
| Per stage visit: the improvements its instruction version contained (A17) | — | added to the existing timeline and instructions audit | Read from the `improvements` field of the visit's `SkillLoaded` | as synced |

No write decision reads a lagging read model. P1–P3 read the record under its lock, and S1–S5 read the improvement's own events.

## Contracts

| Contract | Internal / external | Additive / breaking |
|---|---|---|
| `POST /v1/projects/{project}/skill-improvements` `{id, skill, improves, text, fromAssessment?: {work, stage?, visit?}}` → the improvement | external (viewer) | additive: new endpoint |
| `POST …/{id}/revise` `{text}` | external | additive |
| `POST …/{id}/approve` `{scope: "project"\|"all-projects", revision}` | external | additive |
| `POST …/{id}/withdraw` `{}` | external | additive |
| `GET …/skill-improvements/approved` → `{improvements: [{id, skill, improves, text, revision, scope, approvedAt}]}`, in approval order; project secret only, member token refused 403 | external (gf) | additive. The `syncs` middleware gains this one read route. All other reads still refuse the secret (A15). |
| `GET …/skill-improvements[?skill=&state=]`, `GET …/skill-improvements/{id}` | external (viewer), member token | additive |
| `SkillImproved`, `SkillImprovementNotApplied`, `SkillImprovementDeclined` (stream `skill-improvements`) | external: the server reads them for landings | additive: new event types. Schemas are in the JSON. |
| `SkillImprovementStarted` | internal: synced like every event, but no server reader | additive |
| `SkillLoaded` optional `improvements` | existing, additive optional field | safe in place. Old fixtures decode with the field absent. |
| Errors | `wire.Problem` with stable codes | new codes: `unknown-instruction-version`, `improvement-text-fixed`, `improvement-already-approved`, `improvement-withdrawn`, `improvement-revised-since-read`, `improvement-not-approved`, `conflict` |
| `.applied-improvements` line format | a file contract with the owner, who edits it | new. Readers tolerate unknown outcomes and trailing fields. |

Server-side events (`SkillImprovementProposed`, `Revised`, `Approved`, `Withdrawn`) are internal to the server. gf only ever sees the `ApprovedSkillImprovements` DTO, never these events.

All times are RFC 3339 UTC. Versions are sha256 hex strings. No domain value types appear in the wire package.

## Reactions

There are none. Nothing reacts to an event automatically. Startup is a hook (time-triggered), and the agent's steps are explicit commands. The server builds landings by querying the synced events, not by subscribing to them.

## NFRs

None proposed. The startup GET has a fixed short timeout and never blocks (D8). The change does not affect behaviour under load.

## Follow-ups for later stages

- `CONTEXT.md`: the Project secret entry ("grants nothing else") must say it also reads its project's approved skill improvements. Add the glossary entries Skill improvement, Approved, Applied and Skill improved. This was carried over from grill.
- `docs/SETUP.md`: the record file, how to decline (revert the skill and keep the line) and how to retry (delete the line).

## Decisions for the owner

1. **How a stage visit knows which improvements its instruction version contained (A17, D20).**
   - (a) *Recommended*: add the optional `improvements` field to `SkillLoaded`, holding the record's applied and not-declined ids at load time. It survives the owner's own edits between improvements, and it is an additive change.
   - (b) D9 as written: derive it on the server by chaining `SkillImproved` before → after. This needs no change to `SkillLoaded`, but the chain breaks as soon as the owner edits the skill by hand between two improvements, so later visits show none.
2. **How "declined" is noticed (D15).**
   - (a) *Recommended*: P5. At startup gf marks an applied entry declined when the skill is back at a version from before it. This is deterministic and needs no command. A partial revert, or a revert mixed with other edits, is not noticed, and the entry stays applied.
   - (b) The owner changes the line's outcome word to `declined` by hand. This is exact, but it is a manual step D15 wanted to avoid.
   - (c) Do not track declines. This contradicts D20.
3. **An `in-progress` line left by a crashed or closed session.**
   - (a) *Recommended*: it stays, so P2 blocks that skill, and the brief names it and says "delete the line to retry". This is consistent with D15 and D23.
   - (b) A new startup takes over a line older than one hour. This is automatic, but a long agent run in another repository could be overtaken.
4. **Approval binds to the revision read (S4, rejection `ImprovementRevisedSinceRead`).** This refines D13, so that a revise racing an approve can never approve text the approver didn't see. Recommended: yes.
5. **Record first, history second.** The record and the event log are two stores and can't share a transaction. Writing the record first rules out a double application. A crash in between leaves no `SkillImproved` for that outcome, so the viewer misses one landing. It is logged to `gf.log`. Recommended: accept. The alternative, reconciling the record against the stream at startup, is ambiguous because the record is shared by several repositories (A12).
6. **Withdrawal during a run.** An improvement withdrawn after the startup fetch is still applied by that run, if it gets to it. The window is seconds. Recommended: accept. A4 holds for every later startup. The alternative is a second GET before each `start`, which makes the server a dependency mid-run.
7. **A proposed improvement nobody approves stays proposed forever.** The lifecycle (D13) has no discard. Recommended: leave it as it is for now. The viewer can filter by state. Adding `DiscardSkillImprovement` on `Proposed` is a small later addition.
8. **What counts as vendored (P4, D10).** A skill directory holding `SOURCE.md` (today grilling and domain-modeling). Recommended: yes, rather than a hard-coded list.
9. **What "clone" means in the viewer's landings (D20, A17).** The server can tell projects and owners (the envelope actor) apart, not individual clones. Recommended: show project + owner. Identifying clones would need a new clone id, which is out of scope.
