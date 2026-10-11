# gofast — Claude Code plugin

Drives an agent along gofast's fix-bug path (discovery → specify → implement →
review → integration-testing), with a fixer that sends work back when review
or integration testing finds the code wrong. The `gf` CLI decides where the
work is and refuses invalid moves; this plugin connects it to Claude Code:

- **Stage agents** `gofast:discovery`, `gofast:specify`, `gofast:implement`,
  `gofast:review`, `gofast:integration-testing` and `gofast:fixer`: one
  subagent per stage.
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

Configure the integration suite of the repository you work in, in
`.gofast/checks.json` (without it, integration testing fails), and check it
with `gf checks`:

```json
{
  "integration": { "command": ["go", "test", "-tags=integration", "./..."], "timeout": "15m" },
  "timeout": "10m"
}
```

**Commit checks.json before starting work; changing it during a stage fails
that attempt's scope check.** While it has uncommitted changes, `gf checks`
warns: "checks.json has uncommitted changes. Commit it before starting work;
changing it during a stage fails that attempt's scope check."

Describe how the service must behave under load in `.gofast/nfrs.json`, the
repository's **non-functional requirements** (NFRs). Without it there are
none:

```json
{
  "nfrs": [
    {
      "id": "wallet-transaction-latency",
      "description": "Posting a transaction to a wallet stays fast under concurrent load",
      "areas": ["internal/wallet/**", "internal/ledger/**"],
      "operation": "POST /wallets/{id}/transactions",
      "load": { "vus": 50, "duration": "60s" },
      "targets": { "p95": "150ms", "p99": "400ms", "maxErrorRate": 0.001, "minRps": 200 }
    }
  ]
}
```

`areas` are globs over repository paths (`**` spans directories); a change
there can affect the NFR. `operation` is the HTTP request it is about
(`{name}` segments stand for any value). Each target is optional, but set at
least one. Unknown fields are refused. The NFRs are yours: agents read them,
but no stage may write them, so an agent can't lower a target. Commit
nfrs.json before starting work too; `gf checks` lists the NFRs and warns
while the file has uncommitted changes.

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
      "Bash(gf rework-extend *)",
      "Bash(gf stress *)",
      "Bash(gf abandon *)",
      "Bash(gf log *)",
      "Bash(gf nfrs *)"
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
   exhausted budget escalates; a code failure sends the work to the fixer, and
   reworks are limited too. So the loop always ends.
5. **The owner is needed.** On a human gate (discovery, specify) the session
   stops once the stage is waiting for approval, and tells you which artifact
   to read. When a budget is exhausted, or the work reaches its rework limit,
   it stops and tells you the options. Either way, drive mode ends there:
   after you approve, reject, extend or abandon in your own terminal, run
   `/gofast:drive` to continue. Until you
   do, the session behaves like any other and is never held. Drive mode also
   ends when there is no active work left on the branch.

## Exit checks

`gf submit --passed` runs the stage's exit checks itself; the agent's claim
alone never passes a stage. `gf status` (`current.checks`) and `gf checks`
list them.

| Stage | Required checks |
|---|---|
| discovery | artifact-written |
| specify | artifact-written, specs-fail |
| implement | artifact-written, build-passes, formatted, specs-pass, specs-untouched, suite-passes |
| review | artifact-written, formatted, build-passes, suite-passes |
| integration-testing | artifact-written, integration-passes |
| stress-testing | artifact-written, stress-scripts-valid, nfrs-met |
| fixer | artifact-written |

A stage that may only write its artifact can't fix code, so every check
about code it requires is also required by implement, which can: unformatted
code fails implement, not review. The exceptions are integration-passes and
nfrs-met, integration and stress testing's own purposes.

## The fixer and rework

A failed submission doesn't always mean the stage did its own work badly.
gf tells two kinds apart:

- **Stage-work failure**: the artifact is missing or unchanged, the stress
  scripts aren't valid, a file outside the write scope changed, or a stage
  that can change code (specify, implement) failed. The same stage tries
  again, using an attempt.
- **Code failure**: in a stage that checks code it can't change (review,
  integration testing, stress testing), any other failed check, or the
  agent's own `--failed` (review found a blocking problem). Retrying that
  stage can never fix the code, so the work goes to the **fixer** instead,
  using no attempt. From stress testing, the fixer works in **performance**
  mode; otherwise in **correctness** mode.

The fixer (`gofast:fixer`) finds the root cause and writes it in its
artifact, with what the next visit must do. It ends with
`gf submit --passed --route <specify|implement> "<reason>"`: specify if the
spec was wrong or missed a case, implement if the code is wrong. gf only
accepts an earlier stage on the work's path that can change code; any other
route is refused without using an attempt.

The work then starts a **new visit** of that stage, with its own artifact
(`<stage>-v2.md`), baseline and attempts, and every later stage runs again.
Each of those visits shows the fixer's artifact as `current.reworkOf` in
`gf status --json`, and its agent reads it first. Implement's specs are those
of the latest accepted specify visit: a rework to specify brings new specs, a
rework to implement keeps the old ones.

A work may enter the fixer **3 times**. On the fourth handover it stops at
its rework limit and drive mode ends. The owner runs
`gf rework-extend <n>` (owner only, terminal required) to allow more, or
`gf abandon "<reason>"`.

## Stress testing

Stress testing is an **optional** stage after integration testing: it runs
only if the work decided to include it. **Review decides**, when it passes:
`gf submit --passed --stress <include|skip> "<reason>"` (required in review,
refused elsewhere). No work gets past review without a decision.

If the work's changes (everything changed since its first stage began) touch
the `areas` of an NFR in `.gofast/nfrs.json`, stress testing is
**mandatory**: `--stress skip` is refused, naming the NFRs and their areas,
without using an attempt.

The owner may override review with `gf stress <include|skip> "<reason>"`
(owner only, terminal required) until the work has passed integration
testing; a mandatory stress test still can't be skipped. The owner's
decision stands over review's, unless the change later touches an NFR's
areas. A rework clears the decision: the next review decides again, since
the diff may have changed.

**The agent writes, gf runs and measures.** The stress-testing agent writes
one k6 script per NFR it exercises, at
`.gofast/works/<work>/stress/<nfr-id>.js`: every mandatory NFR needs one,
and it may add others. A script reads the service's address from
`__ENV.BASE_URL` and sends the NFR's `operation`, with whatever data setup it
needs. It must not set the load or judge the run: no `thresholds`,
`handleSummary`, `vus`, `duration`, `iterations`, `stages`, `scenarios`,
`rps` or `minIterationDuration`, and options only as
`export const options = {...}`.

Tell gf how to run your service, in the `stress` section of
`.gofast/checks.json`:

```json
"stress": {
  "start": ["go", "run", "./cmd/server"],
  "ready": "http://localhost:8080/healthz",
  "baseUrl": "http://localhost:8080",
  "startTimeout": "60s",
  "timeout": "15m"
}
```

For each script, `gf submit --passed` starts the service (refusing if
something already answers at `ready`), waits until `ready` answers 2xx, runs
`k6 run` with the NFR's `vus` and `duration` (at least 1s), stops the
service's whole process group, and measures the run from k6's raw output
itself: p95, p99, error rate (no response, or a status of 400 or more) and
requests per second, counting only the NFR's operation during the load (not
`setup()`, not other requests, not requests renamed with a `name` tag).
k6 runs with `--no-thresholds`, no end-of-test summary, an empty config file
and no `K6_*` variables, so nothing but the NFR sets the load. Everything a
run leaves is in `.gofast/runtime/checks/<assignment>-attempt<n>/`
(`<nfr-id>.json.gz`, `<nfr-id>.k6.log`, `<nfr-id>.service.log`).

- **stress-scripts-valid**: every mandatory NFR has a script, every script
  is for an NFR and passes the static check, and every script ran under k6
  and sent its NFR's operation. It also fails, closed, without k6 on `PATH`,
  without a `stress` section, or without NFRs. These are the stage's own
  work (or yours): the stage tries again, using an attempt.
- **nfrs-met**: every scripted NFR met its targets, e.g.
  `wallet-transaction-latency: p95 212ms > 150ms`, or the service never got
  ready. That is a **code failure**: the work goes to the fixer in
  **performance** mode, with the measurements in the reason.

`gf status` (`current.stress` in `--json`) shows, in review, whether stress
testing is mandatory and which NFRs force it; once decided, the decision,
its reason and who made it (a skipped stage is `skipped` in the stage list);
in stress testing, the mandatory NFRs with their script paths and, after an
attempt, each NFR's measurement against its targets. In performance mode,
the fixer names the bottleneck with evidence (the measurements, the run's
files in `current.fixer.stressRunDir`, profiling) before choosing a route.

## Write scopes

| Stage | May write |
|---|---|
| discovery, review, integration-testing, stress-testing, fixer | only the work's artifact directory |
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
submit, and says that the write-scope check starts from then. The exit
checks can't work without the baseline the stage began with, so a
`--passed` then fails with:

> The baseline for '<stage>' is missing (the work was started before this version of gf, or on another machine). The owner should abandon this work with `gf abandon "<reason>"` and start it again.

`gf extend` can't fix this: more attempts don't bring a baseline back.
Abandon the work and start it again on this machine.

The guard also denies any Bash command that mentions `GF_ACTOR`,
`events.jsonl`, `events.lock`, `gf.log`, `.gofast/event`, `.gofast/gf` or
`.gofast/runtime` (which also catches globs like `.gofast/event?.jsonl`), and
any that clears the environment: `env -i`, `env --ignore-environment`,
`env -u`, `unset`.

## How the owner approves

**Approve from your own terminal, not from inside Claude Code.** `gf approve`,
`gf reject`, `gf extend`, `gf rework-extend`, `gf stress`, `gf abandon`,
`gf nfrs adopt` and `gf log accept` are owner-only, and
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
gf rework-extend 1              # at the rework limit; or: gf abandon "<reason>"
gf stress include "<reason>"    # until integration testing passes; or: skip
gf nfrs adopt                   # while design waits: adopt its NFR proposals,
                                # commit .gofast/nfrs.json, then gf approve
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
  `git gc` may prune them after a few weeks. The exit checks then fail with
  the missing-baseline message above, and the owner abandons and restarts the
  work.
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
