# Specify: w-7ec48ee9, visit 2 (rework from fixer-v1, review B1)

## What changed since visit 1
Visit 1's specs (A1–A17) are kept as they are; implement made them pass. This visit adds two specs for the rule-7 surface the review found unprotected: output redirection on the improver's `gf improvement …` commands (B1, blocking), and a leading `GOFAST_SESSION=` assignment (review S3, the optional sibling the fixer named). Both live in cmd/gf/skillimprovements_e2e_test.go, next to `TestGuardAgentActions_TheImproverRunsOnlyGfImprovement`, which is unchanged. No skeletons, no production change.

## Specs
| Scenario | Rule | Test | Level | Fails with |
|---|---|---|---|---|
| A10 | the improver's `gf improvement …` commands may not redirect output to a file; only `/dev/null` (and fd duplication such as `2>&1`) is allowed | cmd/gf/skillimprovements_e2e_test.go TestGuardAgentActions_TheImproverMayNotRedirectOutputToAFile | boundary (PreToolUse hook) | see the case table below: six cases expected denied, got allowed; `2>&1` expected allowed, got denied |
| A10 | the improver may not set `GOFAST_SESSION` on its commands, so it records outcomes only for its own session's improvement | cmd/gf/skillimprovements_e2e_test.go TestGuardAgentActions_TheImproverMayNotActAsAnotherSession | boundary (PreToolUse hook) | expected denied, got allowed (empty hook output) |

### Cases of TestGuardAgentActions_TheImproverMayNotRedirectOutputToAFile
| Command | Expected | Today | Why it fails or holds today |
|---|---|---|---|
| `gf improvement start si-1 > <repo>/CLAUDE.md` | denied | allowed: **fails** | `segments` treats `>` and the path as ordinary words |
| `gf improvement start si-1 ><repo>/CLAUDE.md` (no space) | denied | allowed: **fails** | same |
| `gf improvement applied si-1 --summary x >> ~/.zshrc` | denied | allowed: **fails** | same, append |
| `gf improvement start si-1 2> /tmp/x` | denied | allowed: **fails** | same, stderr to file |
| `gf improvement applied si-1 --summary $(gf improvement start si-1 > f)` | denied | allowed: **fails** | the substituted command is checked, but its redirection isn't |
| `gf improvement start si-1 >\| f` | denied | denied | passes by accident (`\|` splits the segment); pinned so the fix keeps it |
| `gf improvement start si-1 &> f` | denied | denied | passes by accident (`&` splits the segment); pinned |
| `sh -c 'gf improvement start si-1 > <repo>/CLAUDE.md'` | denied | denied | already denied; pinned so a fix that unwraps `sh -c` still denies it |
| `gf improvement applied si-1 --summary 'a > b'` | allowed | allowed | quoted `>` is text, not a redirection; pinned against an over-eager fix |
| `gf improvement applied si-1 --summary "keeps >> as text"` | allowed | allowed | same, double quotes |
| `gf improvement start si-1 > /dev/null` | allowed | allowed | the review's fix direction allows `/dev/null` |
| `gf improvement start si-1 2>/dev/null` | allowed | allowed | same |
| `gf improvement start si-1 2>&1` | allowed | denied: **fails** | `&` splits the segment so `1` looks like a non-gf command; fd duplication names no file (fixer: "N>&M is fine") |

Run: `go test -run 'TestGuardAgentActions_TheImprover' ./cmd/gf/` — both new tests fail on `assert.Equal`/`assert.True` with the command in the message; `TestGuardAgentActions_TheImproverRunsOnlyGfImprovement` and `..._TheImproverWritesOnlyTheSkillItIsImproving` still pass.

## For implement
- Rule 7 (workflow/slices/guardAgentActions/improver.go) must deny any unquoted output redirection (`>`, `>>`, `>|`, `N>`, `&>`, `&>>`) in an improver Bash command, nested and substituted commands included, unless the target is `/dev/null`; `N>&M` is allowed. That needs redirection operators recognised outside quotes, in `segments` (shell.go) or in a scan used only by rule 7. If `segments` changes, the owner-only-command guard tests must stay green.
- Rule 7 must deny a leading `GOFAST_SESSION=` assignment on an improver command (review S3).

## Questions for the owner
- `/dev/null` and `2>&1`: the specs allow `> /dev/null`, `2>/dev/null` and `2>&1`, following the review and the fixer. Option: deny every redirection, `/dev/null` included, for a simpler rule (still within D19). Recommendation: keep them allowed; they write no file and agents add them habitually, so denying them would cause needless refusals. If you prefer the simpler rule, flip those three cases to `true` before implement.
- S3 (`GOFAST_SESSION=`) is included as a separate spec because it is the same rule-7 surface and cheap. If you want this rework limited strictly to B1, drop `TestGuardAgentActions_TheImproverMayNotActAsAnotherSession`.
