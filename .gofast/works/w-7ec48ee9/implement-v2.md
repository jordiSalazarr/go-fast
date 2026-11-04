# Implement: w-7ec48ee9, visit 2 (rework for review B1, fixer-v1, specify-v2)

## What changed since visit 1
Visit 1's implementation of A1–A17 is unchanged. This visit closes rule 7's Bash hole (B1) and review S3, making specify-v2's two new specs pass:
- `TestGuardAgentActions_TheImproverMayNotRedirectOutputToAFile`
- `TestGuardAgentActions_TheImproverMayNotActAsAnotherSession`

## Changes
- `workflow/slices/guardAgentActions/improver.go`
  - New `redirectsToAFile(command)`, used only by rule 7. It scans the line outside quotes (single, double, backslash), and recurses into `$(…)` and backticks, including inside double quotes. Any unquoted `>` is treated as a redirection: `>`, `>>`, `>|`, `N>`, `&>`, `<>`, `>&word`, `>(…)`. The command is denied unless the target is `/dev/null`. `>&N` and `>&-` (descriptor duplication or close) are allowed. An empty or dynamic target (`> $HOME/x`) is denied. `sh -c '…'` and `eval` were already denied because their program is not gf.
  - `onlyGfImprovement` now refuses a command whose prefix (the leading assignments and wrappers `unwrap` drops, so `env GOFAST_SESSION=x gf …` too) sets `GOFAST_SESSION` (`pluginskills.SessionEnv`).
- `workflow/slices/guardAgentActions/shell.go` `segments`: an `&` right after an unquoted `>` now stays in the word instead of ending the command. Before, `2>&1` split off `1` as a separate command. Escaped `\>` does not set the marker. Owner-only detection is unaffected: `gf approve >&2` is still one segment that starts with `gf approve`. The full suite, including the existing owner-only guard tests, passes.

## Checks run
`go build ./...`, `go vet ./...`, `gofmt -l` (clean), `go test ./...` (all green, the new specs included). No spec files touched.

## For review
- Owner decision kept: `/dev/null` and `2>&1` stay allowed.
- Input redirection (`<`) is not denied. It writes nothing, so it is outside D19's write rule.
- S1, S2, S4 and S5 from review v1 are not addressed. They were out of this route.
