# Implement: w-7ec48ee9, visit 3 (rework from fixer-v2, review v2 B1)

## What changed since visit 2
Rule 7's Bash check is no longer a blacklist over the shell reader. It is now one anchored, whole-line allowlist match of the grammar that specify-v3 pinned and the owner approved.

- `workflow/slices/guardAgentActions/improver.go`:
  - New `improvementLine` regexp and `isImprovementCommand`. After optional leading whitespace, the line must be `gf [--dir <arg>] improvement (start|applied|not-applied) <arg>+`, optionally followed by any combination of the literal suffixes ` > /dev/null`, ` 2>/dev/null` and ` 2>&1`, then optional trailing blanks. An `<arg>` is one of three token kinds, separated by spaces or tabs: a plain word of `[A-Za-z0-9_.:/=@,+-]`, `'…'` with no `'` inside, or `"…"` with none of `"`, `$`, backtick or `\` inside. Anything else is denied by construction: `${…}`, `$'…'`, `$"…"`, `$((…))`, `<(…)`, braces, globs, `~`, here-docs, `;`/`&`/`|`/newline chaining, leading assignments (so S3's `GOFAST_SESSION=` stays denied), wrappers (`env`, `sh -c`, `cd … &&`), other subcommands, and every other redirection.
  - Removed `onlyGfImprovement`, `redirectsToAFile`, `redirectionTarget`, `isDescriptor` and `firstGfArg`. Rule 7 no longer used any of them, and nothing else did either.
  - These checks still run first, unchanged: the secret check, the owner-only check and the `protectedNames` check.
  - `onlyImprovementReason` now tells the agent which form is allowed: one plain command, plain words or quoted text (single quotes preferred, no `$`, backtick or `\` in double quotes), and no chaining, substitution, expansion or redirection except the three suffixes.
- I did not change `shell.go`. `segments`, `commands`, `unwrap` and `clearsEnvironment` stay, because rule 1 (owner-only) and guard.go use them. Review v2 N1 is still out of scope.
- `plugin/agents/improve-skills.md` step 4 now writes free text in single quotes, and says to run each command alone with no chaining, piping, substitution, variable expansion or redirection.
- `workflow/slices/startSkillImprovement/start.go`: the brief that `gf improvement start` prints now shows the record commands with single-quoted placeholders, to match.

## Verification
- `go test -run 'TestGuardAgentActions_TheImprover' ./cmd/gf/` passes. That covers the new `…RunsOnlyThePlainImprovementCommands` and the four earlier rule-7 specs.
- Full suite: I ran `go test ./...`. The result is in the submission's `suite-passes` check.

## For review
Check rule 7 by reading the grammar in `improvementLine` (improver.go). The approach no longer depends on the guard parsing bash the same way bash does. The owner accepted these known limits (specify-v3 questions 1 to 3): `--summary "uses $PATH"`, `"a\"b"`, `cd x && gf …`, `env gf …` and globs are denied. A `--dir` path with spaces must be single-quoted.
