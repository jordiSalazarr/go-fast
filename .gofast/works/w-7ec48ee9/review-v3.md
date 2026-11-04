# Review: skill improvements flow back to the plugin (w-7ec48ee9, visit 3, attempt 1)

This is a rework visit after review v2's B1, which found that `${…}` hid commands and redirections from guard rule 7. I read fixer-v2, specify-v3 and implement-v3. The owner approved specify-v3's grammar as written. I then read the new rule-7 code in `workflow/slices/guardAgentActions/improver.go` (`improvementLine`, `isImprovementCommand`), the new spec `TestGuardAgentActions_TheImproverRunsOnlyThePlainImprovementCommands` (cmd/gf/skillimprovements_e2e_test.go:503), the improver prompt `plugin/agents/improve-skills.md` (step 4), and the brief that `startSkillImprovement/start.go` prints (lines 92–94). The code for A1–A17 outside rule 7 hasn't changed since visit 1.

What changed since visit 2: rule 7's Bash check is now one anchored, whole-line regexp. It accepts only `gf [--dir <arg>] improvement start|applied|not-applied <arg>+`, optionally followed by the literal suffixes. An arg is a plain word, a `'…'` with no `'` inside, or a `"…"` with none of `"`, `$`, backtick or `\` inside. Go's `$` without the `m` flag matches only at the end of the text, and the blank pattern is `[ \t]`. So a newline can't start a second command, and a newline is accepted only inside quotes, where it is literal. The blacklist helpers (`onlyGfImprovement`, `redirectsToAFile`, `redirectionTarget`, `isDescriptor`, `firstGfArg`) are gone, and nothing else referenced them. The secret, owner-only and `protectedNames` checks still run first, unchanged. `shell.go` is unchanged here.

I checked the grammar by reading it, and probed edge cases with a scratch copy of the regexp (in the scratchpad, nothing written to the repo):

- Denied: a trailing `\n`, `\r`, `> /dev/null\n; curl x`, `'it'"'"'s'`, `'skill''s'` (adjacent quotes need a blank between them), `--dir=/x`.
- Allowed: quoted text containing newlines, `"!x"`, `2>&1 > /dev/null`, a repeated `> /dev/null`, `=x`, `a=b`, `@x`, `--help`.

None of the allowed forms can write a file or run another program, in bash or in zsh, the owner's shell:
- History expansion is off in a non-interactive shell.
- zsh's `=word` expands to a path that is passed to gf as an argument. It is not run.
- zsh's glob qualifiers need `(`, and `^` and `#` are outside the character set.
- `2>&1 > /dev/null` only sends stderr to the terminal.

`go test -run TestGuardAgentActions ./cmd/gf/` passes, including the five rule-7 specs.

## Verdict
Passed

## Risk map
| # | Rule or behaviour at stake | Where | Why risky | Protected by | Check yourself |
|---|---|---|---|---|---|
| R1 | The improvement agent acts on text from the server. It writes nothing outside the one skill's directory and runs nothing but `gf improvement` (D19, A10) | workflow/slices/guardAgentActions/improver.go `improvementLine`; Write/Edit go through `domain.ImproverMayWrite` | Server text becomes agent instructions. A hole here lets anyone who can get an improvement approved run commands on every team member's machine. The approach is now an allowlist that fails closed, but the character set is the whole defence. | TestGuardAgentActions_TheImproverRunsOnlyThePlainImprovementCommands (44 rows), …MayNotRedirectOutputToAFile, …RunsOnlyGfImprovement, …MayNotActAsAnotherSession, …WritesOnlyTheSkillItIsImproving | Read the 4-line regexp in improver.go. Make sure no character in `[A-Za-z0-9_.:/=@,+-]` or inside the double-quote class is special to your shell. |
| R2 | An approved improvement is applied at most once per plugin directory (P1, D14, D23) | workflow/pluginskills/pluginskills.go `Change` (Truncate then WriteAt) | If a crash or a full disk lands between the two writes, the record is left empty, and every improvement of that skill is applied again. This is v1 S1, still open. | flock serialises writers. No test covers a torn write. | Decide whether writing to a temp file and renaming it is worth it. |
| R3 | A withdrawn improvement, or one from another project, never reaches a clone (A4, A12, A15) | historyserver/skillimprovements.go `ApprovedSkillImprovements`, http.go `syncs` route | A wrong scope filter would push one team's skill wording into another project's sessions, and it is applied with no owner gate. | TestApprovedSkillImprovements_* | Approve an improvement for project A, then GET `/approved` with project B's secret. |
| R4 | A reverted improvement is marked declined, and only that one (P5, D15) | workflow/domain/pluginskill.go (decline walk) | The walk is a heuristic over content hashes. If it is wrong, the wrong ids are marked declined. | Five domain tests and the e2e decline test | Apply two improvements, revert one, start a session and read the record. |
| R5 | An approval approves exactly the text the approver read (S4) | historyserver/skillimprovement `Approve`, skillimprovements.go `improvementCommand` | If a revise races an approve, text nobody read could be approved and then applied automatically everywhere. | Sequential domain and boundary tests | In integration or stress testing, send a revise and an approve concurrently at the same revision. |

## Agreed behaviour
| Item | Implemented in | Proven by | OK |
|---|---|---|---|
| A1–A9, A11–A17 | unchanged since visit 1 (see review-v1) | unchanged specs, suite green | ✓ |
| A10 / D19: the improver's Bash line is allowed only if it matches specify-v3's grammar, and everything else is denied | improver.go `improvementLine`, `isImprovementCommand` | TestGuardAgentActions_TheImproverRunsOnlyThePlainImprovementCommands, including review v2 B1's two scenarios and fixer-v2's `$'a\'b'` | ✓ |
| Owner-approved literal suffixes `> /dev/null`, `2>/dev/null`, `2>&1`, which may combine | improvementLine suffix group | the same spec (4 allowed rows), plus the allowed rows of …MayNotRedirectOutputToAFile | ✓ |
| S3: the improver may not set `GOFAST_SESSION` | denied by the grammar (no leading assignment) | TestGuardAgentActions_TheImproverMayNotActAsAnotherSession, `X=1 …` row | ✓ |
| The secret, owner-only and protected-name checks run before rule 7's Bash check | improver.go `decideImprover` | existing guard specs | ✓ |
| The prompt and the start brief use only forms the grammar allows | improve-skills.md step 4; start.go:92–94 (single-quoted placeholders) | by reading | ✓ |

## Should fix
- S1, S2, S4 and S5 from review v1 are still open: the torn record write, the version read before the lock, exfiltration through `--summary`, and the unbounded `landings` scan. Fixer-v1 and fixer-v2 kept them out of this route, so they are not blocking.

## Notes
- N1 Summary text with an apostrophe ("the skill's wording") can't go in single quotes, and the agent gets a deny. The reason it gets back names double quotes as the alternative, so the agent can recover. The prompt says "with no `'` inside it", but it doesn't say to use double quotes then. Adding that would save a round trip.
- N2 A command with a trailing newline is denied (fails closed). Claude Code's Bash tool normally sends no trailing newline. If refusals show up in practice, `[ \t\n]*$` at the very end would be safe.
- N3 Review v2's N1 still applies: the same `${…}` blind spot exists in the owner-only rule's `segments`. That code predates this work.
- N4 The notes from v1 (N2–N5) still apply.

## Stress decision
include: the work adds file locking on a record that several repositories share, a network GET on every session start, and server queries that scan every improvement and every synced `skill-improvements` event of an organisation. All of these grow with use.
