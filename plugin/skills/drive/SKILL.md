---
# Frontmatter fields: https://code.claude.com/docs/en/skills ("Frontmatter
# reference"). In plugin "gofast" this skill is /gofast:drive.
name: drive
description: Drive the active gofast work along its path, running each stage's agent until the owner is needed.
disable-model-invocation: true
---

You are the thin orchestrator of gofast work. The `gf` CLI holds the logic; you only loop.

1. Run `gf status --json`.
2. If `work` is null there is no active work. If you already ran a stage agent in this drive, the work is completed: tell the owner and stop. Otherwise ask the owner for the work type, one of `fix-bug` (fix a bug), `new-feature` (build something new, designed first) or `modify-behaviour` (change what existing code does, designed first), and a one-line description, run `gf start --type <type> "<description>"`, and go back to 1.
3. If `next.actor` is `agent`, run the `gofast:<current.stage>` subagent, passing it the work description (`work.description`) and the current artifact path (`current.artifact`). Design, the fixer and stress testing are just other stages: `gofast:design`, `gofast:fixer`, `gofast:stress-testing`. When it returns, go back to 1.
4. Otherwise the owner is needed: the stage is waiting for approval, it is escalated, or the work reached its rework limit. Tell the owner in a few lines what happened, which artifact to read (`current.artifact`), and which commands to run in their own terminal (`next.commands`). When design waits for approval, point the owner to the design's `## Decisions for the owner` section, if it has one, and, if `work.nfrProposals` is set, to the NFRs it proposes: the owner runs `gf nfrs adopt`, commits `.gofast/nfrs.json`, then runs `gf approve`. Then stop.
5. Never run `gf approve`, `gf reject`, `gf extend`, `gf rework-extend`, `gf stress`, `gf abandon` or `gf nfrs adopt`; never edit `.gofast/` yourself; never do a stage's work in this session.
