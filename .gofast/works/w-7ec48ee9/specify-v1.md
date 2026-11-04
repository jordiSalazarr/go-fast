# Specify: skill improvements flow back to the plugin (w-7ec48ee9, visit 1, attempt 1)

First attempt. I wrote 72 specs, and every one fails against the current code because the behaviour is missing. They cover all 17 acceptance scenarios (A1–A17) and every command slice in the design. The existing suite still passes: `go test ./...` fails only in the four new spec files.

The specs sit at three levels:
- **Domain**: both aggregates' rules. These are pure, with no fakes.
- **Server boundary**: the reference server over HTTP (`httptest`).
- **gf end to end**: the hooks and `gf improvement …` run in-process against a temp repository. A small fake history server serves only the `GET …/skill-improvements/approved` contract, and the specs use a copy of the plugin.

## Specs

### Domain: `SkillImprovement` (historyserver/skillimprovement/skillimprovement_test.go)
| Scenario | Rule | Test | Fails with |
|---|---|---|---|
| A1 | a new improvement is proposed at revision 1, naming the version it improves | TestProposeSkillImprovement_IsProposedAtRevisionOneForTheVersionItImproves | unexpected errSkeleton |
| A1 | S1: the version must be one the project recorded | TestProposeSkillImprovement_RejectsAVersionTheProjectNeverRecorded | ErrUnknownInstructionVersion not in chain |
| A1 | S1: a version recorded for another skill doesn't count | TestProposeSkillImprovement_RejectsAVersionRecordedForAnotherSkill | same |
| A1 | a retried proposal records nothing | TestProposeSkillImprovement_RepeatedWithTheSameDraftRecordsNothing | unexpected errSkeleton |
| A1 | text is trimmed and holds 1–4000 characters (boundaries 4000/4001, empty) | TestProposeSkillImprovement_TextHoldsFromOneTo4000Characters | errSkeleton / ErrInvalidText not in chain |
| A1 | a proposed improvement is revised (revision + 1) | TestReviseSkillImprovement_ChangesTheTextWhileProposed | unexpected errSkeleton |
| A1 | revising to the current text records nothing | TestReviseSkillImprovement_WithTheCurrentTextRecordsNothing | unexpected errSkeleton |
| A2 | any member, the author included, approves for this project | TestApproveSkillImprovement_ApprovesForItsProjectEvenByItsAuthor | unexpected errSkeleton |
| A2 | S2: approved text is fixed | TestReviseSkillImprovement_RejectsWhenApproved | ErrImprovementTextFixed not in chain |
| A2 | S4: approval binds to the revision read | TestApproveSkillImprovement_RejectsWhenRevisedSinceRead | ErrImprovementRevisedSinceRead not in chain |
| A2 | approving again with the same scope records nothing | TestApproveSkillImprovement_RepeatedWithTheSameScopeRecordsNothing | unexpected errSkeleton |
| A2 | S3: approved once; another scope is refused | TestApproveSkillImprovement_RejectsAnotherScopeWhenAlreadyApproved | ErrImprovementAlreadyApproved not in chain |
| A2 | the approved state holds the text of the revision approved | TestEvolve_AnApprovedImprovementHoldsTheTextItsApproverRead | expected ApprovedImprovement, got nil |
| A3 | approve for all projects | TestApproveSkillImprovement_ApprovesForAllProjects | unexpected errSkeleton |
| A4 | withdraw an approved improvement | TestWithdrawSkillImprovement_WithdrawsAnApprovedImprovement | unexpected errSkeleton |
| A4 | S5: only approved is withdrawn | TestWithdrawSkillImprovement_RejectsWhenOnlyProposed | ErrImprovementNotApproved not in chain |
| A4 | withdrawing again records nothing | TestWithdrawSkillImprovement_RepeatedRecordsNothing | unexpected errSkeleton |
| A4 | S5: a withdrawn improvement is never approved again | TestApproveSkillImprovement_RejectsWhenWithdrawn | ErrImprovementWithdrawn not in chain |
| A4 | S2: a withdrawn improvement's text stays fixed | TestReviseSkillImprovement_RejectsWhenWithdrawn | ErrImprovementTextFixed not in chain |

### Domain: `PluginSkill` (workflow/domain/pluginskill_test.go)
| Scenario | Rule | Test | Fails with |
|---|---|---|---|
| A5, A7 | an improvement the record doesn't list (never written, or line deleted) starts from the current version | TestStartSkillImprovement_StartsAnImprovementTheRecordDoesNotList | unexpected errSkeleton |
| A6 | P1: an applied improvement is not started again | TestStartSkillImprovement_RejectsAnImprovementAlreadyApplied | ErrImprovementAlreadyRecorded not in chain |
| A7 | a declined improvement is not started again | TestStartSkillImprovement_RejectsADeclinedImprovement | same |
| A9 | a not-applied improvement is not retried | TestStartSkillImprovement_RejectsAnImprovementRecordedAsNotApplied | same |
| A6 | an unreadable line counts as recorded | TestStartSkillImprovement_RejectsAnImprovementOnAnUnreadableLine | same |
| A5 | P2: another session's line blocks the skill | TestStartSkillImprovement_RejectsWhileAnotherSessionHoldsTheSkill | ErrSkillImprovementInProgress not in chain |
| A11 | P2: one improvement per skill at a time | TestStartSkillImprovement_RejectsAnotherImprovementWhileOneIsInProgress | same |
| A5 | start repeated by the holder records nothing | TestStartSkillImprovement_RepeatedByTheHoldingSessionRecordsNothing | unexpected errSkeleton |
| A11 | P4: a vendored skill is out of scope | TestStartSkillImprovement_RejectsAVendoredSkill | ErrSkillOutOfScope not in chain |
| A5 | applied records the versions before and after | TestImproveSkill_RecordsTheVersionsBeforeAndAfter | unexpected errSkeleton |
| A8 | already held: applied with after = before | TestImproveSkill_RecordsANoChangeApplicationWhenTheSkillAlreadyHeldIt | unexpected errSkeleton |
| A5 | P3: another session can't record the outcome | TestImproveSkill_RejectsAnotherSessionsImprovement | ErrImprovementNotInProgress not in chain |
| A5 | P3: nothing in progress, no outcome | TestImproveSkill_RejectsAnImprovementNotInProgress | same |
| A5 | the same outcome repeated by the holder records nothing | TestImproveSkill_RepeatedAfterItsOutcomeIsRecordedRecordsNothing | unexpected errSkeleton |
| A9 | conflict recorded as not applied, with the reason, at the unchanged version | TestReportImprovementNotApplied_RecordsAConflictWithItsReason | unexpected errSkeleton |
| A10 | refusal recorded as not applied, refused | TestReportImprovementNotApplied_RecordsARefusal | unexpected errSkeleton |
| A9 | P3 for not-applied | TestReportImprovementNotApplied_RejectsAnotherSessionsImprovement | ErrImprovementNotInProgress not in chain |
| A7 | P5: back at the before-version → declined | TestDeclineSkillImprovement_DeclinesAnAppliedImprovementTheOwnerReverted | expected one Declined, got nil |
| A7 | P5: reverting the latest declines only it | TestDeclineSkillImprovement_RevertingTheLatestDeclinesOnlyIt | same |
| A7 | P5: reverting to before the first declines all | TestDeclineSkillImprovement_RevertingToBeforeTheFirstDeclinesAll | elements differ |
| A8 | P5: a no-change application is never declined | TestDeclineSkillImprovement_NeverDeclinesANoChangeApplication | expected one Declined, got nil |
| A7 | a decline is recorded once | TestDeclineSkillImprovement_AnEntryDeclinedIsNotDeclinedAgain | Evolve returned nil |
| A5 | applied → entry applied, skill idle | TestEvolvePluginSkill_AnAppliedImprovementIsRecordedAndTheSkillIsIdle | Evolve returned nil |
| A11 | pending grouped by skill, skills by earliest approval, then approval order | TestPendingSkillImprovements_GroupsBySkillInApprovalOrder | expected groups, got nil |
| A6, A7, A9 | whatever its outcome (applied, declined, not-applied, unreadable), a listed id is not pending | TestPendingSkillImprovements_SkipsWhatTheRecordLists | expected si-5 only, got nil |
| A11 | vendored and missing skills are skipped | TestPendingSkillImprovements_SkipsVendoredAndMissingSkills | expected aggregate-design only, got nil |

### Server boundary (historyserver/skillimprovements_test.go)
Most of these fail first at the Given's `POST …/skill-improvements` (expected 201, got 404): the resource doesn't exist yet.
| Scenario | Rule | Test | Fails with |
|---|---|---|---|
| A1 | a member writes an improvement of a recorded version, from an assessment; text trimmed, revision 1, proposed | TestProposeSkillImprovement_AMemberWritesAnImprovementOfARecordedVersion | expected 201, got 404 |
| A1 | unknown version refused with `unknown-instruction-version` | TestProposeSkillImprovement_RefusesAVersionTheProjectNeverRecorded | expected code, got "" |
| A1 | revise edits a proposed text | TestReviseSkillImprovement_AMemberEditsAProposedImprovement | 404 |
| A2 | the author approves for this project | TestApproveSkillImprovement_ItsAuthorApprovesItForThisProject | 404 |
| A2 | revise refused once approved (`improvement-text-fixed`) | TestReviseSkillImprovement_IsRefusedOnceApproved | 404 |
| A2 | approve refused when revised since read | TestApproveSkillImprovement_IsRefusedWhenRevisedSinceRead | 404 |
| A3 | all projects: served to every acme project, including one created later, and not to globex | TestApprovedSkillImprovements_AnAllProjectsApprovalReachesEveryProjectOfTheOrganisation | 404 |
| A4 | withdrawn: no longer served to anyone | TestWithdrawSkillImprovement_AWithdrawnImprovementIsNoLongerServed | 404 |
| A12 | project-only: not served to payments | TestApprovedSkillImprovements_AProjectOnlyApprovalIsNotServedToAnotherProject | 404 |
| A15 | the secret reads only approved, non-withdrawn improvements of its project, in approval order, with the DTO fields | TestApprovedSkillImprovements_TheSecretReadsOnlyItsProjectsApprovedImprovements | 404 |
| A15 | a member's token is refused 403 on `/approved` | TestApprovedSkillImprovements_RefusesAMembersToken | 404 |
| A15 | the secret still can't read the members' list or an improvement | TestApprovedSkillImprovements_TheSecretStillReadsNothingElse | 404 |
| A17 | per improvement: landings (project, owner, outcome, reason, before, after) from synced `skill-improvement.*` events | TestSkillImprovementsOfOrganisation_ShowsWhereAnImprovementLanded | 404 |
| A17 | per visit: the timeline's instruction shows the `improvements` of its SkillLoaded | TestServer_ATimelineShowsTheImprovementsEachVisitsInstructionVersionContained | expected [si-1 si-2], got nil |

### gf end to end (cmd/gf/skillimprovements_e2e_test.go)
| Scenario | Rule | Test | Fails with |
|---|---|---|---|
| A5, A7 | at startup with sync configured: GET `/approved` with `Bearer <project secret>`; the brief says to run `gofast:improve-skills`; `export GOFAST_SESSION=<session_id>` goes to CLAUDE_ENV_FILE | TestBriefAgentOnSessionStart_BriefsTheSessionToApplyAnApprovedImprovementTheRecordLacks | no request; brief lacks improve-skills; env lacks GOFAST_SESSION |
| A6 | the record lists it, so the check runs but nothing is applied | TestBriefAgentOnSessionStart_AppliesNothingTheRecordAlreadyLists | expected 1 fetch, got 0 |
| A7 | startup marks a reverted improvement `declined` in the record, appends `skill-improvement.declined`, and doesn't re-apply it | TestDeclineSkillImprovement_AtStartupARevertedImprovementIsRecordedAsDeclined | line still `applied`; no event |
| A13 | unreachable: one line in the brief, and the session goes on (exit 0, drive hint kept) | TestBriefAgentOnSessionStart_TellsTheSessionWhenTheServerIsUnreachable | brief lacks the line |
| A14 | startup fetches once; resume, clear and compact never fetch (table) | TestBriefAgentOnSessionStart_ChecksForImprovementsOnStartupOnly | startup: expected 1, got 0 |
| A5 | `gf improvement start si-1` prints the skill dir and approved text, and writes `si-1 in-progress <before> - abc123` | TestStartSkillImprovement_MarksTheImprovementInProgressAndHandsTheAgentItsText | Unknown command "improvement" |
| A5 | `gf improvement applied si-1 --summary …` writes `si-1 applied <before> <after>…` and appends `skill-improvement.applied` with the contract payload; the record stays outside the version | TestImproveSkill_RecordsTheImprovementWithTheVersionsBeforeAndAfter | Unknown command |
| A8 | applied with no edit → before = after | TestImproveSkill_RecordsAnImprovementTheSkillAlreadyHeldWithNoChange | Unknown command |
| A9 | `not-applied --conflict "<reason>"` writes `si-1 not-applied <v> - … # <reason>` and the contract event; the skill is unchanged | TestReportImprovementNotApplied_RecordsTheConflictAndItsReason | Unknown command |
| A10 | `not-applied --refused` → cause `refused` | TestReportImprovementNotApplied_RecordsARefusal | Unknown command |
| A5, A10 | guard: `gofast:improve-skills` writes only inside the in-progress skill's directory; it may not write the record, another skill, an agent definition or the repo | TestGuardAgentActions_TheImproverWritesOnlyTheSkillItIsImproving | today every write is denied ("no active gofast work"), the skill's own files included |
| A10 | guard: the improver's Bash runs only `gf improvement …` (curl, rm, git commit denied) | TestGuardAgentActions_TheImproverRunsOnlyGfImprovement | curl, rm and git commit are allowed today |
| A16, A12 | a SkillLoaded records `improvements`: the record's applied, not-declined ids, whichever project applied them; the version is recorded too | TestObserveSkillLoads_ALoadRecordsTheImprovementsTheSkillContains | `improvements` absent |

## Rules without a spec
- **A13, without sync nothing happens**: this already holds (TestHookSessionStart_EndToEnd and TestHooks_AreSilentInARepositoryWithoutGofast). A spec would pass, so it isn't part of this change.
- **A16, a load records the new version after an improvement**: this already holds, because observeSkillLoads re-reads the version on every load (design, and TestInstructions_AStageVisitRecordsTheVersionsThatInstructedIt). The new part, `improvements`, is specced.
- **A4, the clone that applied keeps the change**: nothing in the design undoes a record or a skill on withdrawal. No command reacts to a withdrawal, so there is no behaviour to pin beyond "withdrawn is no longer served" (specced).
- **A5, A11, A9, A10: agent behaviour** (editing by intent, deciding conflict or refusal, the final summary and `git diff`): this is in the `plugin/agents/improve-skills.md` prompt, which Go tests can't execute. gf's side is specced: the brief, the commands, the record, the history and the guard.
- **Optimistic concurrency on `skill_improvement_events`** (retry, 409 `conflict`) and the **advisory lock on the record**: no deterministic seam exists before implement chooses the store code. The domain rules they protect (S4, P2) are specced. Integration testing should cover a revise racing an approve.
- **The 3 s startup timeout**: an NFR-like limit. A deterministic spec would need a slow fake and a real wait, so I left it for integration testing.
- **Viewer UI** (forms and pages in app.js): the JSON it reads is specced at the boundary (A17).

## Skeletons
Only new files. Each function body is a single zero-value return or `errSkeleton`. Domain errors are typed **constants** (`Rejection`, `PluginSkillRejection`), because `skeletons-only` allows no `var` but `errSkeleton`. Their `Error()` returns `""` for now, and implement fills it in (e.g. `return string(r)`). Implement may turn them into `errors.New` vars if it keeps the names.

- `historyserver/skillimprovement/skillimprovement.go` (new package, the server-side domain):
  - **Values**: `ID`, `ProjectID`, `SkillName`, `InstructionVersion`, `Member`, `Revision`, `Text` and `ParseText`; `Scope` (`ScopeUnset`, `ThisProject`, `AllProjects`); `AssessmentRef`, `RecordedVersionFact`, `Draft`.
  - **States**: `SkillImprovement` (sealed), `NoImprovement`, `ProposedImprovement`, `ApprovedImprovement` and `WithdrawnImprovement`, sharing an embedded `Proposal`. `Proposal` has no methods, so no command is promoted.
  - **Events**: `SkillImprovementProposed`, `Revised`, `Approved` and `Withdrawn`.
  - **Methods**: `NoImprovement.Propose`, `ProposedImprovement.Revise` and `ProposedImprovement.Approve`, `ApprovedImprovement.Withdraw`.
  - **Switching functions**: `ProposeSkillImprovement`, `ReviseSkillImprovement`, `ApproveSkillImprovement` and `WithdrawSkillImprovement` (each returns `[]Event`: empty when idempotent), plus `Evolve`.
  - **Errors**: `ErrInvalidText`, `ErrUnknownInstructionVersion`, `ErrImprovementTextFixed`, `ErrImprovementAlreadyApproved`, `ErrImprovementWithdrawn`, `ErrImprovementRevisedSinceRead`, `ErrImprovementNotApproved`, `ErrNoImprovement`.
- `workflow/domain/pluginskill.go` (the `PluginSkill` aggregate, in the workflow domain package; it declares `errSkeleton` for package domain):
  - **Values**: `SkillImprovementID`, `SessionID`; `ImprovementOutcome` (`OutcomeUnreadable` = zero, `InProgress`, `Applied`, `NotApplied`, `Declined`); `NotAppliedCause` (`CauseConflict`, `CauseRefused`); `RecordEntry`, `ApprovedImprovementFact`.
  - **States**: `PluginSkill` (sealed), `IdleSkill`, `ImprovingSkill` (`InProgress` entry), and `PluginSkillOf` (state from a record).
  - **Events**: `SkillImprovementStarted`, `SkillImproved`, `SkillImprovementNotApplied`, `SkillImprovementDeclined`.
  - **Methods**: `IdleSkill.StartSkillImprovement`, `ImprovingSkill.ImproveSkill`, `ImprovingSkill.ReportImprovementNotApplied`.
  - **Switching functions**: `StartSkillImprovement`, `ImproveSkill`, `ReportImprovementNotApplied`, `DeclineSkillImprovement`, `EvolvePluginSkill`.
  - **Pending**: `LocalSkill`, `PendingSkill`, `PendingSkillImprovements`.
  - **Errors**: `ErrImprovementAlreadyRecorded`, `ErrSkillImprovementInProgress`, `ErrSkillOutOfScope`, `ErrImprovementNotInProgress`.
  - The new events implement `domain.Event`. Implement must add their codec entries (`skill-improvement.started`, `.applied`, `.not-applied`, `.declined`, per the contracts) on the `skill-improvements` stream.
- `workflow/remote/wire/skillimprovements.go` (types and constants only):
  - **Requests**: `ProposeSkillImprovement`, `ReviseSkillImprovement`, `ApproveSkillImprovement`.
  - **Responses**: `SkillImprovement` (with `Landings`), `SkillImprovements`, `Landing`, `ApprovedSkillImprovements`, `ApprovedSkillImprovement`, `AssessmentRef`.
  - **Constants**: the scope constants (`project`, `all-projects`), the states, the landing outcomes, and the new problem codes.

Implement also changes existing production code. None of it is a skeleton:
- the routes and the `syncs` read route in `http.go`, the store table, and audit and timeline (`improvements` on `wire.Instruction`);
- the SkillLoaded codec field, the SessionStart hook, the guard, and observeSkillLoads;
- a new `gf improvement start|applied|not-applied` command;
- `plugin/agents/improve-skills.md`.

## Questions for the owner
1. **Status codes of the domain refusals.** The design gives the problem codes but not the HTTP statuses. The specs assert only the code. Recommendation: 409 for state rejections (`improvement-text-fixed`, `-already-approved`, `-withdrawn`, `-revised-since-read`, `-not-approved`, `conflict`), and 400 or 422 for `unknown-instruction-version`.
2. **How the improvement agent learns the order of pending improvements (A11).** The design has the brief tell the session to run `gofast:improve-skills`, but it doesn't say whether the brief lists the pending ids or a `gf improvement` subcommand lists them. The e2e brief specs assert only that `gofast:improve-skills` is named, and the order is pinned at the domain level (`PendingSkillImprovements`). Recommendation: the brief lists the pending ids in order, so no extra command is needed.
3. **Session on applied lines.** The format's `[<session>]` is optional. The specs only check the prefix `<id> <outcome> <before> <after|->`, and expect the session on the `in-progress` line (`… - abc123`). Recommendation: keep the session on every line gf writes, because the "repeated by the holder" idempotency reads it.
4. **The value types are string-kinded** (`SkillImprovementID`, `SessionID`, `SkillName`, `ID`, …) rather than structs with unexported fields. That way the specs can build them while skeleton constructors return zero values. Parsing stays at the edge (`ParseText` is specced). Recommendation: accept, or let implement add `Parse…` constructors next to them without changing the types.
