# gofast — Claude Code plugin

Drives an agent along gofast's fix-bug path (discovery → specify → implement →
review → integration-testing). The `gf` CLI decides where the work is and
refuses invalid moves; this plugin connects it to Claude Code:

- **Stage agents** `gofast:discovery`, `gofast:specify`, `gofast:implement`,
  `gofast:review`, `gofast:integration-testing`: one subagent per stage.
- **`/gofast:drive`**: the orchestrator. It reads `gf status`, runs the right
  stage agent and repeats until the owner is needed.
- **Hooks** (`gf hook <event>`): the agent can't approve, can't touch the
  event log, can't write outside its stage's scope, and can't quietly stop
  mid-stage.

## Install

```sh
go install ./cmd/gf          # from the go-fast repo; puts gf on your PATH
claude --plugin-dir ./plugin # local use, from the go-fast repo
```

Every hook runs `gf` from `PATH`. **If `gf` isn't on `PATH`, the hooks do
nothing**: no guard, no briefing, no keep-going. Check with `which gf`.

The hooks are silent in repositories without a `.gofast/` directory, so the
plugin is harmless elsewhere. `gf start` creates `.gofast/` in a repository.

## Recommended permission rules

The guard hook parses Bash commands on a best-effort basis. Add deny rules as
the hard layer, in `~/.claude/settings.json`
([permission rule syntax](https://code.claude.com/docs/en/permissions)):

```json
{
  "permissions": {
    "deny": [
      "Bash(gf approve *)",
      "Bash(gf reject *)",
      "Bash(gf extend *)",
      "Bash(gf abandon *)"
    ]
  }
}
```

A trailing ` *` also matches the bare command (`gf approve`). Claude Code
applies deny rules to every part of a compound command, including subshells
and command substitutions. The rules don't match other ways of running the
same binary, such as `/full/path/gf approve` or `go run ./cmd/gf approve`.
The guard hook and `GF_ACTOR=agent` (below) cover those.

## How a session flows

1. **Session start.** Claude is briefed with the text of `gf status` and the
   line "To drive this work, run /gofast:drive." The hook also appends
   `export GF_ACTOR=agent` to the session's environment file, so `gf` itself
   refuses owner-only commands from Claude's shell.
2. **`/gofast:drive`.** With no active work, Claude asks you for a work type
   and a one-line description and runs `gf start`. Otherwise it runs the
   current stage's agent.
3. **Stage agents.** Each one reads `gf status --json`, does its stage inside
   its write scope, writes its artifact to `.gofast/works/<work>/<stage>-v<n>.md`,
   and ends with `gf submit --passed` or `gf submit --failed "<reason>"`. An
   agent that tries to finish without submitting is sent back to submit.
4. **The loop.** While the stage is open, the driving session isn't allowed to
   stop: it runs the next agent. Failed submissions use attempts, and an
   exhausted budget escalates, so the loop always ends.
5. **The owner is needed.** On a human gate (discovery, specify) the session
   stops once the stage is waiting for approval, and tells you which artifact
   to read. When a budget is exhausted, it stops and tells you the options.

Write scopes per stage:

| Stage | May write |
|---|---|
| discovery, review, integration-testing | only the work's artifact directory |
| specify | tests (`*_test.go`, files under `testdata/`) and artifacts |
| implement | anything except gofast's own files in `.gofast/` |

Nothing in `.gofast/` outside the work's artifact directory is ever writable,
and no agent may run Bash commands mentioning `GF_ACTOR`, `events.jsonl`,
`events.lock` or `gf.log`.

## How the owner approves

**Approve from your own terminal, not from inside Claude Code.** Claude Code's
shell has `GF_ACTOR=agent`, so `gf approve` typed there (including with the
`!` prefix) is refused. In a separate terminal, in the repository:

```sh
gf status                       # what is waiting, and which artifact to read
gf approve                      # or: gf reject "<feedback>"
gf extend 2                     # after an escalation; or: gf abandon "<reason>"
```

Then go back to Claude Code and run `/gofast:drive` to continue.

## Finding: does `GF_ACTOR` reach subagents?

**Yes.** Checked with Claude Code 2.1.286. The SessionStart hook wrote
`export GF_ACTOR=agent` to `CLAUDE_ENV_FILE`, and `printenv` printed
`GF_ACTOR=agent` both from the main session's Bash and from the Bash tool
inside a general-purpose subagent. The hooks docs don't say this either way.
The guard hook covers subagents in any case.

## Runtime files

`.gofast/runtime/` holds local session bookkeeping: which sessions are driving
and which assignment each stage agent started on. It is listed in
`.gofast/.gitignore`, and it is never part of the event log.
