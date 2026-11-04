# Implement: skill improvements flow back to the plugin (w-7ec48ee9, visit 1, attempt 2)

**Changed since attempt 1.** Attempt 1 failed only `skeletons-filled`, and the cause was a false positive: gf's own checker, `workflow/exitchecks/skeletons.go`, declares an identifier named `errSkeleton`, so the check flagged its own source. That would happen on any implement in this repository. I renamed the private constant to `skeletonErrName`. Its value and behaviour are unchanged, and the exitchecks tests pass. Nothing else changed.

Attempt 1 was the first. All 72 specs pass, and so does the whole suite (`go test ./...`, `go vet ./...` and gofmt are clean). I didn't touch any spec file, and every skeleton is filled. I built what design-v1 describes, with the owner's accepted answers: 409 for state rejections and 422 for `unknown-instruction-version`; the brief lists the pending ids in order; gf writes the session on every record line; and the string-kinded value types stay, with `ParseSkillImprovementID` added next to them.

## Domain (built first)
- **`historyserver/skillimprovement/skillimprovement.go`** holds the `SkillImprovement` aggregate.
  - Rules S1–S5: Propose, Revise, Approve and Withdraw on their states, a switch function per command, and `Evolve`. `ParseText` trims the text and accepts 1–4000 runes.
  - Idempotency:
    - a propose on an existing id records nothing;
    - a revise to the same text records nothing;
    - an approve with the same scope records nothing;
    - a withdraw that is already withdrawn records nothing.
  - Rejections are the typed `Rejection` constants, and `Error()` returns their text.
- **`workflow/domain/pluginskill.go`** holds the `PluginSkill` aggregate.
  - Rules P1–P5: Start, Improve and ReportNotApplied, plus `DeclineSkillImprovement` and `EvolvePluginSkill`.
  - Helpers:
    - `PluginSkillOf` rebuilds the state from a record;
    - `Entries` lists the entries, with the in-progress one last;
    - `AppliedImprovements` lists the ids that are applied and not declined.
  - `PendingSkillImprovements` groups the pending improvements by skill. Skills are ordered by their earliest approval, then each skill's improvements by approval order.
  - Additions beyond the skeleton:
    - `ParseSkillImprovementID`;
    - the `ImproveSkillsAgent` constant;
    - `SkillHeldBy`, `LeftInProgress` and `ImproverMayWrite`. The last is the guard's pure rule: only inside the held skill's directory, and never its `.applied-improvements`.
  - P5 walks the record in order. Once the current version equals the before-version of an applied or declined entry, every later applied entry that changed the skill is declined. A no-change entry is never declined.
- `domain.SkillLoaded` gains `Improvements []SkillImprovementID`.

## Adapters
- **Server** (`historyserver/skillimprovements.go`, routes in `http.go`):
  - A new append-only table, `skill_improvement_events`, with `UNIQUE (improvement, version)` and triggers that refuse updates and deletes. It is added to `store.go`'s schema.
  - Each command runs load → decide → insert at version + 1. A unique conflict retries up to 3 times, then returns 409 `conflict`.
  - Domain rejections map to `wire.Problem` codes. 400 `invalid` covers bad ids, skills, versions, text and scopes. An improvement of another organisation reads as 404.
  - Reads:
    - `GET …/skill-improvements[?skill=&state=]` and `GET …/{id}` are member-only, and `{id}` includes landings from the synced `skill-improvement.applied`, `.not-applied` and `.declined` events of the organisation's projects;
    - `GET …/approved` is secret-only, through `syncs`. It returns improvements approved for this project, or for all projects of its organisation, that are not withdrawn, in approval order. A member's token gets 403.
  - Propose returns 201 when it creates the improvement and 200 for a retried id.
  - `improvements` reaches the timeline's `wire.Instruction` and the instructions audit's `wire.InstructionUse`. Both are optional fields.
- **Viewer** (`app.js`, `index.html`, `app.css`):
  - a "Skill improvements" list, filterable by state and skill;
  - a detail page with landings and the revise, approve (scope plus the revision read) and withdraw actions;
  - a propose form. The id is made once per form, as `si-` plus random hex. The form opens from "Propose an improvement" on a skill's version, and from a visit with a went-wrong assessment, which links that visit's skills;
  - instruction chips show `with si-…`.
  - No Go test covers the viewer. I checked app.js's syntax only.
- **gf**:
  - Codec:
    - `skill-improvement.started`, `.applied`, `.not-applied` and `.declined` on the new `eventlog.SkillImprovementsStream` (`skill-improvements`). A cause gf doesn't know is read as `conflict`;
    - `SkillLoaded.improvements` is optional.
  - `remote.Client.ApprovedSkillImprovements`.
  - **`workflow/pluginskills`** is a new bounded-context package, shared by the hook, the guard, observeSkillLoads and the three commands. It does:
    - record parsing and writing. Lines it can't read, blank lines and comments are kept verbatim. Lines it can read are rewritten in place;
    - the exclusive `flock` on the record file (`Change`, which loads, decides, evolves and writes);
    - local skill listing (vendored means the skill has `SOURCE.md`);
    - the fetched list;
    - plugin-root resolution;
    - appending to the history.
  - **New slices**:
    - `declineSkillImprovement`, run by the startup hook;
    - `startSkillImprovement` (`gf improvement start`);
    - `improveSkill` (`gf improvement applied [--summary]`);
    - `reportImprovementNotApplied` (`gf improvement not-applied --conflict|--refused "<reason>"`).
    - The `gf improvement` group is wired in `cmd/gf/main.go`, and the domain errors and `pluginskills.Error` map to messages in `errors.go`.
    - Each command writes the record first and the history second (Decision 5). `applied` and `not-applied` then run a quiet best-effort sync, `synchistory.AfterSkillImprovement` with a 10 s client. Its only output is one stderr line when it fails.
    - Events are recorded as the clone's owner (from git config), falling back to the agent. The viewer shows landings by owner.
  - **`briefAgentOnSessionStart`**, only on `source == "startup"` with sync configured and `$CLAUDE_PLUGIN_ROOT` known:
    1. Appends `export GOFAST_SESSION=<id>` to `CLAUDE_ENV_FILE`. This happens only then, so the existing no-sync hook test still sees only `GF_ACTOR`.
    2. Declines what the owner reverted.
    3. Fetches with a 3 s timeout.
    4. Keeps what it fetched.
    5. Puts its lines before the status brief:
       - the pending ids per skill, in order;
       - "run gofast:improve-skills without asking";
       - show the summary and the diff, and leave it uncommitted;
       - in-progress lines left by other sessions, with "delete the line to retry";
       - record lines gf could not read.
    - When the server can't be reached the brief gets the one line, and the same goes for a refusal or a missing secret.
  - **Guard**: a new rule 7 in `guardAgentActions/improver.go`. For `gofast:improve-skills`:
    - Bash: every simple command must be `gf improvement …`. That includes nested commands and command substitutions. Rules 1–3 and the secret rule still apply;
    - Write, Edit and NotebookEdit: only through `domain.ImproverMayWrite`;
    - the stage-scope and catalog rules don't apply to it.
  - The improver is not treated as a stage agent: `keepAgentOnStage`'s subagent-start skips it, and so does `observeSkillLoads`'s AgentStarted. Otherwise it would be told to "submit before finishing" on an active work.
  - **`observeSkillLoads`** sets `Improvements` on every versioned SkillLoaded, from the skill's record.
  - **Plugin**: `plugin/agents/improve-skills.md`, the improvement agent's instructions:
    - order;
    - start;
    - edit by intent: already held, conflict, or refused when the text asks for anything but a wording edit;
    - record the outcome;
    - one final summary;
    - it never commits.
- **Docs**:
  - `CONTEXT.md`: the Project secret entry is sharpened, and I added Skill improvement, Approved, Applied and Skill improved.
  - `docs/SETUP.md` §6.4, item 7, covers the record, decline and retry. The remote and historyserver package docs now name the one read that goes back.

## Deviations and notes for review
- **The fetched list lives at `.gofast/runtime/skill-improvements.json`**, not at `.gofast/skill-improvements.json` as the design says. `runtime/` is already gitignored, which meets the design's "local, gitignored, not synced" without touching `.gofast/.gitignore`.
  - `gf improvement` takes the plugin root from `$CLAUDE_PLUGIN_ROOT`. When that isn't set, it uses the root recorded in this file.
- **The improvement agent can't run `git diff`**: its Bash is limited to `gf improvement …` (D19). So the brief tells the main session to show the diff (`git -C <plugin> diff`) once the agent returns.
- **`start` takes the approved text only from the fetched list.** `applied` and `not-applied` find the skill by scanning the records for the id, so they work without a fetch, as the specs do.
- **Only `applied` and `not-applied` sync afterwards, not `start`.** `SkillImprovementStarted` is internal and is sent with the next sync. Declines found at startup are also sent with the next sync, not synced from the hook.
- **The decline needs sync configured.** It runs in the startup check, so it only runs when sync is configured (the design's startup flow, step 1). A refusal or a missing secret gets its own one-line brief, alongside the "could not be reached" line.
- **Integration testing should cover the cases no deterministic spec covers:**
  - a revise racing an approve (409 `revised-since-read` or `conflict`);
  - two sessions starting the same skill under the record lock;
  - the 3 s timeout against a slow server;
  - the viewer forms in a browser.
