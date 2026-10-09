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
- **`gf` itself** backs the hooks up: owner-only commands need a terminal,
  `gf submit` checks the stage's write scope with git, and the event log is
  tamper-evident.

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
      "Bash(gf abandon *)",
      "Bash(gf log *)"
    ]
  }
}
```

A trailing ` *` also matches the bare command (`gf approve`). Claude Code
applies deny rules to every part of a compound command, including subshells
and command substitutions. The rules don't match other ways of running the
same binary, such as `/full/path/gf approve` or `go run ./cmd/gf approve`.
The guard hook and `gf`'s own owner check (below) cover those.

## How a session flows

1. **Session start.** Claude is briefed with the text of `gf status` and the
   line "To drive this work, run /gofast:drive." The hook also appends
   `export GF_ACTOR=agent` to the session's environment file, one of the two
   layers that make `gf` refuse owner-only commands from Claude's shell (see
   "How the owner approves").
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
   Either way, drive mode ends there: after you approve, reject, extend or
   abandon in your own terminal, run `/gofast:drive` to continue. Until you
   do, the session behaves like any other and is never held. Drive mode also
   ends when there is no active work left on the branch.

## Write scopes

| Stage | May write |
|---|---|
| discovery, review, integration-testing | only the work's artifact directory |
| specify | tests (`*_test.go`, files under `testdata/`) and artifacts |
| implement | anything except gofast's own files in `.gofast/` |

Nothing in `.gofast/` outside the work's artifact directory is ever writable.
Scopes are enforced twice:

1. **While the agent works**, the guard hook denies Write, Edit and
   NotebookEdit outside the scope, for fast feedback.
2. **At `gf submit`**, by git, however the files were written (`sed -i`,
   `cat >`, `cp`, a script). When a stage's assignment opens, gf records a
   *baseline*: a git tree of the working state, tracked and untracked
   non-ignored files, built with a temporary index (the real index is never
   touched), in `.gofast/runtime/baselines/<assignment>`. `gf submit` diffs
   the working state against it. Any changed file outside the scope turns
   `--passed` into a failed attempt naming the files ("Changed files outside
   the 'specify' write scope: main.go."), and is added to the reason of a
   `--failed`. The failed attempt uses one of the stage's attempts as usual.
   Committing doesn't hide a change: the comparison is with the stage's start,
   so the agent has to undo it. gf's own committed files
   (`.gofast/events.jsonl`, `.gofast/.gitignore`, `.gofast/.gitattributes`)
   are left out of the diff; the tamper check covers the log.

If a stage has no baseline (it began before this check existed, or on
another machine), gf takes one when the next stage agent starts or at
submit, and says that the check starts from then.

The guard also denies any Bash command that mentions `GF_ACTOR`,
`events.jsonl`, `events.lock`, `gf.log`, `.gofast/event`, `.gofast/gf` or
`.gofast/runtime` (which also catches globs like `.gofast/event?.jsonl`), and
any that clears the environment: `env -i`, `env --ignore-environment`,
`env -u`, `unset`.

## How the owner approves

**Approve from your own terminal, not from inside Claude Code.** `gf approve`,
`gf reject`, `gf extend`, `gf abandon` and `gf log accept` are owner-only, and
`gf` checks two things before they reach the workflow:

1. **stdin is a terminal** (the isatty check, via the terminal driver).
   Claude Code's Bash tool, in the main session and in subagents, runs without
   one, so this holds whatever the environment says. Redirecting from
   `/dev/null` doesn't pass it either.
2. **`GF_ACTOR` is not `agent`.** The session-start hook sets it in Claude's
   shell. On its own it is easy to remove (`env -i`), which is why the
   terminal check exists; it stays as an extra layer.

Commands typed in Claude Code, including with the `!` prefix, are refused. In
a separate terminal, in the repository:

```sh
gf status                       # what is waiting, and which artifact to read
gf approve                      # or: gf reject "<feedback>"
gf extend 2                     # after an escalation; or: gf abandon "<reason>"
```

Then go back to Claude Code and run `/gofast:drive` to continue.

## The event log is tamper-evident

After every append, `gf` records the SHA-256 of `.gofast/events.jsonl`, its
size and the git `HEAD` in `.gofast/runtime/log.sum`. Every read compares the
log with it. If the log was changed outside gf, by a hand edit, by lines
appended by something else, or by dropping events gf wrote (e.g. `git stash`
or `git checkout -- .gofast/events.jsonl`):

- write commands refuse: "The event log was changed outside gf since its last
  write. Inspect .gofast/events.jsonl (git diff) and run `gf log accept` from
  your own terminal if the change is legitimate."
- `gf status` and the hooks still read it; status starts with a warning line,
  and `gf status --json` has `logWarning`.

`gf log accept` (owner only, terminal required) accepts the log as it is now.

Git's own changes are not reported: after `git pull`, `git merge`, a checkout
of another branch or `git rebase`, the log is accepted silently when it is
the one committed at the new `HEAD`, and either `HEAD` moved since gf's last
write or the log gf last wrote is committed on a local branch (so nothing is
lost). A missing `log.sum` is recreated on the next read.

## Accepted limits

The threat model is a cooperative agent taking shortcuts, not an adversary.
These are known and out of scope:

- **A deliberately faked terminal.** `script` or `unbuffer` give a command a
  pseudo-terminal, which passes the terminal check. Running gf that way from
  an agent's shell is deliberate misuse, not a shortcut.
- **An agent rewriting or deleting `log.sum`.** The guard denies Bash commands
  that name `.gofast/runtime`, but a script can still write it; a deleted seal
  is recreated from the log as it is.
- **An agent committing a hand-edited log.** Moving `HEAD` to a commit that
  holds the edited log looks like a legitimate git change, as does
  `git reset` to an earlier commit.
- **Changes the owner makes during a stage** count as the stage's changes at
  submit time.
- **Very old baselines.** Baseline trees aren't referenced by any branch, so
  `git gc` may prune them after a few weeks; the check then starts again from
  a new baseline, with a note.
- **A crash between an append and its seal** is reported as a change on the
  next command; check the log and run `gf log accept`.

## Finding: is there a terminal in Claude Code's Bash?

**No.** Checked in a live Claude Code session: `[ -t 0 ]` fails: stdin is not
a terminal. In that session, with `GF_ACTOR` unset,
`env -i PATH=/usr/bin:/bin "$(command -v gf)" approve` was refused with "Only
the owner can approve, from their own terminal. Run `gf approve` there." The
same command with `< /dev/null` was refused too.

## Finding: does `GF_ACTOR` reach subagents?

**Yes.** Checked with Claude Code 2.1.286. The SessionStart hook wrote
`export GF_ACTOR=agent` to `CLAUDE_ENV_FILE`, and `printenv` printed
`GF_ACTOR=agent` both from the main session's Bash and from the Bash tool
inside a general-purpose subagent. The hooks docs don't say this either way.
The guard hook covers subagents in any case.

## Runtime files

`.gofast/runtime/` holds local bookkeeping: which sessions are driving, which
assignment each stage agent started on, each stage visit's working-tree
baseline (`baselines/`), and the event log's seal (`log.sum`). It is listed in
`.gofast/.gitignore`, and it is never part of the event log.
