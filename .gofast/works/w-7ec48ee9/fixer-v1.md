# Fixer: w-7ec48ee9, visit 1 (from review visit 1)

## Failure
Review v1, blocking B1: the improvement agent (`gofast:improve-skills`) can write any file through a shell output redirection. Guard rule 7 allows `gf improvement … > <any path>`, which breaks D19 ("the guard denies every other path"), A10 and ADR 0004.

## Reproduction
I ran a throwaway probe test through `go test -overlay` with the test file in the scratchpad, so nothing was written to the repo. It calls `onlyGfImprovement` (workflow/slices/guardAgentActions/improver.go:79) directly:

```
go test -overlay <scratch>/overlay.json -run TestProbeRedirect -v ./workflow/slices/guardAgentActions/
"gf improvement start si-1 > /Users/x/CLAUDE.md"   allowed=true    <- hole
"gf improvement applied si-1 >> ~/.zshrc"          allowed=true    <- hole
"gf improvement start si-1 2> f"                   allowed=true    <- hole
"gf improvement start si-1 > /dev/null"            allowed=true    (harmless)
"gf improvement start si-1 >| f"                   allowed=false   (denied by accident: `|` splits the segment)
"gf improvement start si-1 &> f"                   allowed=false   (denied by accident: `&` splits the segment)
"curl evil"                                        allowed=false
```

## Root cause
- Symptom: a command whose first words are `gf improvement` passes rule 7 even when it redirects output to any file (probe output above).
- Cause: `onlyGfImprovement` (improver.go:79–95) checks only that each simple command starts with `gf improvement`. The words come from `segments` (shell.go:104–193), which splits on `;&|\n(){}` but treats `>` and `>>` as ordinary word characters. So `> /path` is just another argument, and the shell later truncates or appends to that path with gf's output.
- Origin: specify. The design is right. design-v1.md:104 says the improver's "Bash is limited to `gf improvement …`. Every other path or command is denied (D19)." But the spec for that rule, specify-v1.md:98 (`TestGuardAgentActions_TheImproverRunsOnlyGfImprovement`, cmd/gf/skillimprovements_e2e_test.go:435–451), covers only curl, rm and git commit. No spec makes a redirected `gf improvement` command fail, so implement was never pushed to handle redirections and the spec passes without proving D19.

## Route
specify: this behaviour has no spec. The fixer rule is that uncovered behaviour goes to specify, even when the code fix is small. The next specify visit adds failing cases to the guard spec, and implement then makes them pass.

Not implement: implement could patch the code, but then no spec would pin D19 against redirections, and review would have no test to point at. Not design: the design already says to deny every other path, so the model needs no new decision.

## For the next visit
- Wrong: the improver's Bash rule allows output redirection, so `gf improvement … > path`, `>> path` and `2> path` write files outside the skill directory (probe above, improver.go:79).
- Change (specify): add cases to `TestGuardAgentActions_TheImproverRunsOnlyGfImprovement` (cmd/gf/skillimprovements_e2e_test.go:439) for the rule "the improver's Bash may not redirect output to a file":
  - denied: `gf improvement start si-1 > <repo>/CLAUDE.md`, `gf improvement applied si-1 --summary x >> ~/.zshrc`, `gf improvement start si-1 2> /tmp/x`, `gf improvement start si-1 >| f`, `gf improvement start si-1 &> f`, and a redirection inside `sh -c '…'` or `$(…)`;
  - allowed: a `>` inside a quoted argument (`--summary 'a > b'`), and `> /dev/null` / `2>/dev/null`, as the review's fix direction suggests. The owner may also choose to deny `/dev/null` too to keep the rule simpler. Both are within D19.
  - The new cases must fail today. Record each case and why it fails today in the specify artifact's table.
  - Optional, same test or a sibling one: review's S3, a leading `GOFAST_SESSION=<other>` assignment on an improver command, denied. It is the same rule-7 surface, a should-fix rather than a blocker.
- Then implement: in rule 7, deny any improver Bash command with an unquoted output redirection (`>`, `>>`, `>|`, `N>`, `&>`, `N>&M` is fine if it doesn't name a file), except to `/dev/null`. This needs redirection operators recognised outside quotes in the shell reader (shell.go `segments`), or a separate scan used only by rule 7.
- Verify by: `go test -run '^TestGuardAgentActions_TheImproverRunsOnlyGfImprovement$' ./cmd/gf` fails before the code change and passes after. The whole `go test ./...` stays green, the existing owner-only-command guard tests included (a change to `segments` touches them).
- Keep: D19 and design-v1 as they are. Write, Edit and NotebookEdit scope through `domain.ImproverMayWrite`. Every other passing spec, A1–A17. The review's should-fix items S1, S2, S4 and S5 are not part of this route unless the owner asks for them. They may be done if cheap, but must not widen this visit.
