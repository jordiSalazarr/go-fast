# Setting up gofast in a repository

This guide takes you from nothing to a first piece of work driven by gofast in
one of your repositories. You'll install the `gf` CLI, load the Claude Code
plugin, prepare the repository, and learn what to do each time gofast stops
for you.

Two words used throughout:

- **Your repository**: the Go project you want to work on with gofast.
- **The go-fast clone**: your local checkout of
  `github.com/jordiSalazarr/go-fast`, which holds `gf` and the plugin.

---

## Contents

1. [What you end up with](#1-what-you-end-up-with)
2. [Prerequisites](#2-prerequisites)
3. [Install gf](#3-install-gf)
4. [Load the Claude Code plugin](#4-load-the-claude-code-plugin)
5. [Add the deny rules](#5-add-the-deny-rules)
6. [Prepare your repository](#6-prepare-your-repository)
7. [Run your first work](#7-run-your-first-work)
8. [Your job when gofast stops](#8-your-job-when-gofast-stops)
9. [What to commit](#9-what-to-commit)
10. [Setup checklist](#10-setup-checklist)
11. [Troubleshooting](#11-troubleshooting)
12. [Command reference](#12-command-reference)

---

## 1. What you end up with

```
 ┌────────────────────────────┐        ┌───────────────────────────────┐
 │ Claude Code session        │        │ Your own terminal (the owner) │
 │  /gofast:drive             │        │  gf status                    │
 │   └─ runs gofast:<stage>   │        │  gf approve / gf reject       │
 │      agents, one per stage │        │  gf extend / gf abandon …     │
 │  hooks → gf hook <event>   │        │                               │
 └─────────────┬──────────────┘        └───────────────┬───────────────┘
               │  gf submit, gf status                 │ owner-only commands
               ▼                                       ▼
        ┌────────────────────────────────────────────────────────┐
        │ gf  (the workflow: paths, gates, budgets, exit checks) │
        │ .gofast/ in your repository (event log, config, docs)  │
        └────────────────────────────────────────────────────────┘
```

- **`gf`** holds all the workflow logic. It decides which stage the work is
  on, runs each stage's exit checks, and refuses invalid moves.
- **The `gofast` plugin** runs one agent per stage and loops until you are
  needed. Its hooks stop agents from approving their own work, writing
  outside their stage, or quitting mid-stage.
- **You** are the owner. Only you approve, reject, extend, abandon or adopt
  NFRs, and only from a real terminal outside Claude Code.

Work follows one of three paths:

| Work type | Path (`[…]` = optional) | Your approval gates |
|---|---|---|
| `fix-bug` | discovery → specify → implement → review → integration-testing → [stress-testing] | discovery, specify |
| `new-feature` | discovery → design → specify → implement → review → integration-testing → [stress-testing] | discovery, design, specify |
| `modify-behaviour` | same as new-feature; discovery must state current and expected behaviour | discovery, design, specify |

---

## 2. Prerequisites

| Need | Why | Check |
|---|---|---|
| **Go 1.24+** | to build `gf`; the exit checks run `go build`, `go vet`, `go test`, `gofmt` | `go version` |
| **git** | gofast tracks every stage's changes with git | `git --version` |
| **Claude Code** | runs the plugin, its agents and its hooks | `claude --version` |
| **Your repository is a Go module in a git repository** | the exit checks are Go-specific | `go build ./...` in it |
| **git identity in your repository** | `gf` knows you, the owner, by `user.name` and `user.email` | `git config user.name` |
| **k6** (optional) | only if you use stress testing | `k6 version` ([k6.io](https://k6.io)) |

If `git config user.name` prints nothing, set it in your repository:

```sh
git config user.name "Your Name"
git config user.email "you@example.com"
```

---

## 3. Install gf

From the go-fast clone:

```sh
cd /path/to/go-fast
go install ./cmd/gf
```

This puts `gf` in `$(go env GOBIN)`, or `$(go env GOPATH)/bin` when GOBIN is
empty (usually `~/go/bin`).

**That directory must be on the `PATH` that Claude Code sees.** Every plugin
hook runs `gf` from `PATH`. If Claude Code can't find `gf`, the hooks quietly
do nothing: no guard, no briefing, no keep-going. Check in a new terminal:

```sh
which gf        # e.g. /Users/you/go/bin/gf
gf --help       # lists start, submit, status, approve, …
```

If `which gf` prints nothing, add the Go bin directory to your shell profile
(`~/.zshrc` or `~/.bashrc`), then open a new terminal:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"
```

**After pulling a new version of go-fast**, run `go install ./cmd/gf` again.
The plugin and `gf` must come from the same version.

---

## 4. Load the Claude Code plugin

The plugin lives in `plugin/` in the go-fast clone. Load it when you start
Claude Code in your repository:

```sh
cd /path/to/your-repo
claude --plugin-dir /path/to/go-fast/plugin
```

To avoid typing the path every time, add an alias to your shell profile:

```sh
alias claude-gofast='claude --plugin-dir /path/to/go-fast/plugin'
```

**Check it loaded.** In the Claude Code session:

- `/gofast:drive` shows up when you type `/gofast`.
- `/agents` lists `gofast:discovery`, `gofast:design`, `gofast:specify`,
  `gofast:implement`, `gofast:review`, `gofast:integration-testing`,
  `gofast:stress-testing` and `gofast:fixer`.

The plugin is harmless in other repositories. Its hooks do nothing where
there is no `.gofast/` directory.

### The owner's skills

The plugin ships your skills, and each stage agent preloads the ones for its
stage, by their full name (`gofast:review`, `gofast:aggregate-design`, …):

| Agent | Preloads |
|---|---|
| `discovery` | discovery, code-layout |
| `design` | aggregate-design, functional-domain, value-types, schema-evolution, reads-and-reactions |
| `plan` | code-layout |
| `characterize` | specify (the shape of a spec only) |
| `specify` | specify, functional-domain |
| `implement` | aggregate-design, functional-domain, value-types, code-layout, schema-evolution, reads-and-reactions |
| `optimize` | functional-domain, code-layout |
| `review` | review, aggregate-design, functional-domain, value-types, code-layout, schema-evolution, reads-and-reactions |
| `fixer` | fixer, discovery |
| `event-modelling`, `measure`, `integration-testing`, `stress-testing` | none |

Grill runs in the main session and loads `gofast:grilling` and
`gofast:domain-modeling` itself (vendored from `mattpocock/skills`, MIT, with
their `LICENSE` and `SOURCE.md`). Where a skill and an agent's protocol
differ on gf commands, write scopes, checks or artifact paths, the protocol
wins.

**The plugin's copies are the source of truth.** If you have skills with the
same names elsewhere (for example in your claude.ai account), turn those off,
so the session and the agents only see the plugin's versions. To change a
skill, edit it in `plugin/skills/<name>/SKILL.md`.

**Every version is recorded.** When a stage agent starts, gf records which
version of the agent's definition and of each skill it preloads instructed
it; a skill loaded later with the Skill tool is recorded with its version
too. A version is the SHA-256 of the skill's files (every file in its
directory, dotfiles excepted) or of the agent's file, so editing a skill
makes a new version on its own: there is no version number to bump. gf
keeps a copy of each version in `.gofast/instructions/<version>/`, so the
log's history stays readable after you change a skill or never commit the
edit. `gf status` shows the current visit's versions, and `gf checks` verifies
that every copy still matches its version. If gf can't find the plugin
(`$CLAUDE_PLUGIN_ROOT` unset) or a skill comes from another plugin, it is
recorded without a version.

---

## 5. Add the deny rules

The guard hook already blocks agents from running owner-only commands. Deny
rules add a second, harder layer in Claude Code itself. Add them to
`~/.claude/settings.json` (merge them into any `permissions` you already have):

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

A third layer is built into `gf`: owner-only commands refuse to run unless
stdin is a real terminal. Claude Code's Bash tool never has one, so these
commands only work in your own terminal. That includes commands typed with
the `!` prefix inside Claude Code: those are refused too.

---

## 6. Prepare your repository

Do this once per repository, **before** you start the Claude Code session.
The hooks only switch on in a repository that already has a `.gofast/`
directory when the session starts.

### 6.1 Configure the exit checks: `.gofast/checks.json`

Create `.gofast/checks.json` at the root of your repository:

```json
{
  "timeout": "10m",
  "integration": {
    "command": ["go", "test", "-tags=integration", "./..."],
    "timeout": "15m"
  }
}
```

| Field | Required | Meaning |
|---|---|---|
| `timeout` | no (default `10m`) | the longest any check's command may run |
| `integration.command` | **yes, for integration testing** | your integration suite, as a program and its arguments (no shell). Without it, integration testing always fails |
| `integration.timeout` | no (defaults to `timeout`) | the longest the integration command may run |

If you don't have a separate integration suite yet, use the unit tests:
`"command": ["go", "test", "./..."]`.

#### Optional: stress testing

To use stress testing, tell gf how to start your service. gf starts it,
waits until it is ready, runs the agent's k6 scripts against it, and measures
the results itself:

```json
{
  "timeout": "10m",
  "integration": { "command": ["go", "test", "-tags=integration", "./..."] },
  "stress": {
    "start": ["go", "run", "./cmd/server", "-addr", "127.0.0.1:8080"],
    "ready": "http://127.0.0.1:8080/healthz",
    "baseUrl": "http://127.0.0.1:8080",
    "startTimeout": "60s",
    "timeout": "15m"
  }
}
```

| Field | Required | Meaning |
|---|---|---|
| `stress.start` | yes | the command that starts your service |
| `stress.ready` | yes | an `http(s)://` URL that answers 2xx once the service is up |
| `stress.baseUrl` | yes | where the k6 scripts send requests (scripts read it as `__ENV.BASE_URL`) |
| `stress.startTimeout` | no (default `1m`) | how long the service has to become ready |
| `stress.timeout` | no (default `15m`) | the longest one k6 run may take |

Without a `stress` section, or without `k6` on `PATH`, the stress-testing
stage fails if a work includes it.

Unknown fields are refused, so typos show up straight away.

### 6.2 Optional: your NFRs in `.gofast/nfrs.json`

Non-functional requirements (NFRs) say how an operation must behave under
load. When a work changes files in an NFR's `areas`, stress testing becomes
**mandatory** for that work. Without this file there are no NFRs, and review
decides on its own whether to stress test.

```json
{
  "nfrs": [
    {
      "id": "wallet-transaction-latency",
      "description": "Posting a transaction stays fast under concurrent load",
      "areas": ["internal/wallet/**", "internal/ledger/**"],
      "operation": "POST /wallets/{id}/transactions",
      "load": { "vus": 50, "duration": "60s" },
      "targets": { "p95": "150ms", "p99": "400ms", "maxErrorRate": 0.001, "minRps": 200 }
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `id` | letters, digits, `-`, `_` |
| `description` | optional prose |
| `areas` | globs over repository paths; `**` spans directories |
| `operation` | an HTTP method and path; `{name}` segments match any value |
| `load` | `vus` virtual users for `duration` (at least `1s`) |
| `targets` | at least one of `p95`, `p99` (durations), `maxErrorRate` (0 to 1), `minRps` |

**Always set `minRps`.** Without it, a script that sleeps between requests
can keep latency low by sending little load. `gf checks` warns about every
NFR without one.

NFRs belong to you: agents can read them but no stage may change them. On
new-feature and modify-behaviour work, the design stage may *propose* NFRs;
you adopt them with `gf nfrs adopt` (see [section 8](#8-your-job-when-gofast-stops)).

### 6.3 Optional: observability for discovery and the fixer

Discovery and the fixer inherit the session's tools, MCP tools included, so
they can read the logs, traces and metrics of the running system when they
investigate an error, an incident or slowness. To give them an
observability server:

1. Add it to your repository's `.mcp.json`. For example, Logfire's MCP
   server, with a **read-only** token taken from an environment variable:

   ```json
   {
     "mcpServers": {
       "logfire": {
         "command": "uvx",
         "args": ["logfire-mcp@latest"],
         "env": { "LOGFIRE_READ_TOKEN": "${LOGFIRE_READ_TOKEN}" }
       }
     }
   }
   ```

   Export `LOGFIRE_READ_TOKEN` in your shell. Never commit the token.

2. Approve the server and allow its tools in `.claude/settings.json`, so the
   subagents don't stall on permission prompts:

   ```json
   {
     "enabledMcpjsonServers": ["logfire"],
     "permissions": { "allow": ["mcp__logfire"] }
   }
   ```

3. Use read-only credentials. The agents are told to use these tools
   read-only and to keep secrets and personal data out of their artifacts,
   which are committed; but that is advice. The token is the control.

### 6.4 Check the configuration

```sh
gf checks
```

It prints the configuration as gf will use it, whether `k6` is on `PATH`,
the checks each stage requires, and your NFRs. It warns while `checks.json`
or `nfrs.json` has uncommitted changes, and about NFRs without `minRps`. Fix
anything it reports.

### 6.5 Commit

```sh
git add .gofast/checks.json .gofast/nfrs.json   # nfrs.json only if you made one
git add .mcp.json .claude/settings.json          # only if you added an observability server
git commit -m "Configure gofast"
```

**Commit before starting work.** gofast compares every stage with the
working tree as it was when the stage began. If you change `checks.json` or
`nfrs.json` while a stage is open, that stage's next submission fails because
it "changed files outside its write scope".

### 6.6 Start from a clean working tree

Commit or stash your own changes before starting work. Anything you change
while a stage is open counts as that stage's change.

---

## 7. Run your first work

Use two windows:

- **Window A**: Claude Code in your repository, with the plugin loaded.
- **Window B**: a normal terminal in your repository. This is where you act
  as the owner.

In window A:

```sh
cd /path/to/your-repo
claude --plugin-dir /path/to/go-fast/plugin
```

The session starts with a briefing from `gf status`. Then run:

```
/gofast:drive
```

With no active work on the branch, Claude asks for:

- **the work type**: `fix-bug`, `new-feature`, `modify-behaviour`,
  `refactor` or `improve-performance`;
- **a one-line description**, e.g. "The server listens on 8081 instead of
  8080".

It then runs `gf start --type <type> "<description>"` and starts the first
stage's agent. You can also start the work yourself in window B and then run
`/gofast:drive`:

```sh
gf start --type fix-bug "The server listens on 8081 instead of 8080"
```

**Work belongs to the git branch it starts on**, and a branch has at most one
active work. Create a branch first if you want to keep `main` clean:

```sh
git switch -c fix/port
```

From here the drive loops by itself. Each agent does its stage, writes its
artifact to `.gofast/works/<work>/<stage>-v<n>.md`, and submits. `gf` runs
the stage's exit checks. The loop continues until you are needed.

New feature work (path v3) runs: event modelling (only with an event model),
discovery, which maps your input onto the code and ends with
`## Questions for grill`, then **grill, live with you in the session**, then
design, specify, implement, review, integration testing, and stress testing
when review includes it. Works started on an earlier path finish on it (`gf
status --json` shows `work.pathVersion`).

---

## 8. Your job when gofast stops

When the drive stops, it tells you what happened, which file to read, and
which commands to run. **Run them in window B, never inside Claude Code.**
After you act, go back to window A and run `/gofast:drive` again.

At any time, `gf status` (window B) shows where the work is and what comes
next.

### A stage waits for your approval

This happens on discovery, design (new-feature and modify-behaviour only)
and specify. Read the artifact the drive names (`current.artifact` in
`gf status`), then do one of:

```sh
gf approve
gf reject "what must change"     # uses one of the stage's 3 attempts
```

What to look for:

| Stage | Read | Approve when |
|---|---|---|
| discovery | `discovery-v<n>.md` | it describes the problem correctly. On modify-behaviour it has `## Current behaviour` and `## Expected behaviour` sections (gf checks they exist and aren't empty) |
| design | `design-v<n>.md` and `design-v<n>.json` | the event model, aggregates, invariants, commands, read models, slices and contracts are right. Answer anything under `## Decisions for the owner` (approve the recommended option, or reject saying which one you want) |
| specify | `specify-v<n>.md` and the new `*_test.go` files | the tests describe the right behaviour. gf has already checked that they compile and fail. On designed work, gf has checked that every added or changed command and automation slice has a `Test<SliceName>…` test |

### Design proposes NFRs

If the design proposes NFRs, `gf status` shows "NFR proposals pending
adoption", and the drive tells you. To adopt them:

```sh
gf nfrs adopt          # shows what it adds and replaces, then asks [y/N]
git add .gofast/nfrs.json && git commit -m "Adopt the design's NFRs"
gf approve
```

- This only works while design waits for your approval.
- `--yes` skips the question.
- A proposed NFR with an existing id replaces it, and a new id adds one.
  Proposals never remove NFRs.
- **If you want to reject the design after adopting**, restore
  `.gofast/nfrs.json` first (`git checkout .gofast/nfrs.json`, or revert the
  commit). Otherwise design's next submission fails its scope check.

### A stage is escalated

A stage used all 3 of its attempts. Read `gf status` (it shows the last
failure) and the stage's artifact, then either give it more attempts or stop
the work:

```sh
gf extend 2
gf abandon "why"
```

### The work reached its rework limit

When review, integration testing or stress testing finds the code wrong,
the work goes to the **fixer**, which sends it back to design, specify or
implement. A work may do this 3 times. After that:

```sh
gf rework-extend 1
gf abandon "why"
```

### You disagree with review's stress-testing decision

Until the work passes integration testing, you can override review:

```sh
gf stress include "why"
gf stress skip "why"      # refused if a change touches an NFR's areas
```

### The event log was changed outside gf

If `.gofast/events.jsonl` was edited by hand, gf refuses to change the
workflow. Inspect it (`git diff .gofast/events.jsonl`), and if the change is
legitimate:

```sh
gf log accept
```

Normal git operations (pull, merge, checkout, rebase) don't trigger this when the log they bring is the one committed at the new HEAD.

---

## 9. What to commit

gofast creates these in your repository:

| Path | Commit it? | What it is |
|---|---|---|
| `.gofast/checks.json` | **yes**, before starting work | your exit-check configuration |
| `.gofast/nfrs.json` | **yes**, before starting work (and after adopting) | your NFRs |
| `.gofast/events.jsonl` | **yes**, with your branch | the event log: every step of every work. Git merges it across branches without conflicts |
| `.gofast/.gitignore`, `.gofast/.gitattributes` | **yes** | created by gf; keep local files out of git and set up the event log's merge driver |
| `.gofast/works/<work>/` | **yes**, with your branch | the artifacts: what each stage did and why |
| `<decisionsDir>/` (default `docs/decisions/`) | **yes**, with your branch | grill's decision documents, and the index gf generates |
| `GLOSSARY.md`, `GLOSSARY-MAP.md`, `docs/adr/*.md` | **yes**, with your branch | the glossary and ADRs grill writes while it interviews you |
| `.gofast/instructions/<version>/` | **yes**, with your branch | a copy of each version of the plugin's skills and agents that instructed a stage, written by gf's hooks; the event log names them by version |
| `.gofast/runtime/`, `.gofast/gf.log`, `.gofast/events.lock` | no (already ignored) | local bookkeeping: baselines, check logs, stress runs, the log's seal, gf's diagnostic log |

You can commit whenever you like, including mid-work. gofast compares each
stage with the working tree when the stage began, so committing never hides a
change.

---

## 10. Setup checklist

- [ ] `go version` is 1.24 or newer.
- [ ] `which gf` prints a path, in a new terminal.
- [ ] `claude --plugin-dir /path/to/go-fast/plugin` loads, and `/gofast:drive` exists.
- [ ] The deny rules are in `~/.claude/settings.json`.
- [ ] `git config user.name` is set in your repository.
- [ ] `go build ./...` and `go test ./...` pass in your repository.
- [ ] `.gofast/checks.json` has an `integration.command`.
- [ ] (Optional) `.gofast/nfrs.json` exists, and every NFR sets `minRps`.
- [ ] (Optional, stress testing) `checks.json` has a `stress` section, and `k6` is on `PATH`.
- [ ] `gf checks` shows no warnings.
- [ ] `.gofast/` is committed, and the working tree is clean.
- [ ] You start Claude Code **after** `.gofast/` exists.

---

## 11. Troubleshooting

| Symptom or message | Cause and fix |
|---|---|
| Agents write anywhere, and nothing is briefed at session start | The hooks can't find `gf`. Check `which gf` in the environment you launch Claude Code from, and fix your `PATH` (section 3). Or `.gofast/` didn't exist when the session started: restart Claude Code. |
| "Only the owner can approve, from their own terminal." | You ran an owner command inside Claude Code, `!` prefix included. Run it in window B. |
| "gf could not tell who you are." | Set `git config user.name` and `user.email` in your repository. |
| "Not inside a git repository." | Run gf from inside your repository, or pass `--dir /path/to/repo`. |
| "You are not on a branch." | You're on a detached HEAD. Check out a branch. |
| "There is already active work on branch …" | Finish it, or run `gf abandon "<reason>"`, or switch to another branch. |
| "integration-passes: No integration command is configured." | Add `integration.command` to `.gofast/checks.json` and commit it. |
| "Changed files outside the '<stage>' write scope: …" | Something changed files the stage may not touch. If it was you, undo the change: the comparison is with the stage's start, so committing doesn't help. |
| "skeletons-only: <file>: an existing production file changed; specify may only add skeleton files" | Specify edited code that existed before. It may only add new skeleton files; the change belongs to implement. Undo it. |
| "skeletons-only: <file>: func <name> has logic" (or "panics", or "var <name> is not `var errSkeleton = …`") | A skeleton does more than declare. Its functions may only be empty or return zero values or `errSkeleton`; it may declare `errSkeleton` once per package. Specify tries again. |
| "skeletons-filled: errSkeleton is still in <files>" | Implement left a skeleton unfilled. It tries again until no non-test Go file uses `errSkeleton`. |
| "review-verdict-consistent: … the verdict says Passed but ## Blocking lists B1" | Review passed while its artifact lists blocking findings (or lacks `## Verdict`, `## Risk map` or `## Agreed behaviour`). Review tries again: with blocking findings it must submit `--failed`, which sends the work to the fixer. |
| `gf checks` warns ".gofast/instructions/<version> changed after gf kept it" | A kept copy was edited, so it no longer matches the version the event log names. Restore it with git (`git checkout -- .gofast/instructions/<version>`). To change a skill, edit `plugin/skills/`, never a kept copy. |
| `gf checks` warns "has uncommitted changes" | Commit `checks.json` / `nfrs.json` before starting work. |
| "The baseline for '<stage>' is missing" | The work was started with an older gf or on another machine, or `git gc` pruned an old baseline. Run `gf abandon "<reason>"` and start the work again. |
| "The exit-check configuration … is invalid" / "The NFRs … are invalid" | The message names the field. Fix the file; `gf checks` shows the result. |
| Stress testing fails with k6 not found | Install k6 and make sure it's on `PATH` (`gf checks` shows it). |
| "The event log was changed outside gf …" | See [section 8](#the-event-log-was-changed-outside-gf). |
| Anything else: "Something went wrong." | Details are in `.gofast/gf.log`. |

### Specs for code that doesn't exist yet

Specs must compile and fail. When they need types or functions that don't
exist yet, specify writes **skeletons**: new non-test Go files that declare
only what the specs need, with bodies that return zero values or
`errSkeleton`. `skeletons-only` checks them, and implement must fill every
one (`skeletons-filled`). You don't need to commit a skeleton yourself any
more.

---

## 12. Command reference

Run `gf <command> --help` for details. *Owner* commands work only in your own
terminal.

| Command | Who | What it does |
|---|---|---|
| `gf start --type <fix-bug\|new-feature\|modify-behaviour> "<description>"` | anyone | starts work on the current branch |
| `gf status [--json]` | anyone | where the work is, what to read, what comes next |
| `gf checks` | anyone | shows and validates `checks.json` and `nfrs.json` |
| `gf submit --passed` / `--failed "<reason>"` | agents | submits the current stage; a pass runs its exit checks |
| `gf approve` | owner | approves a stage waiting for you |
| `gf reject "<feedback>"` | owner | sends it back (uses an attempt) |
| `gf extend <n>` | owner | gives an escalated stage more attempts |
| `gf rework-extend <n>` | owner | allows more trips through the fixer |
| `gf stress <include\|skip> "<reason>"` | owner | overrides review's stress-testing decision |
| `gf nfrs adopt [--yes]` | owner | adopts the NFRs a design proposes, while design waits for you |
| `gf abandon "<reason>"` | owner | stops the work for good |
| `gf log accept` | owner | accepts an event log changed outside gf |
| `--dir <path>` | any command | runs against another repository |

For how gofast works inside (exit checks per stage, write scopes, the
fixer, stress testing, the event log's tamper check), read
[`plugin/README.md`](../plugin/README.md).
