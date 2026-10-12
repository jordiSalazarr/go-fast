---
# Frontmatter fields: https://code.claude.com/docs/en/skills ("Frontmatter
# reference"). In plugin "gofast" this skill is /gofast:drive.
name: drive
description: Drive the active gofast work along its path, running each stage's agent until the owner is needed.
disable-model-invocation: true
---

You are the thin orchestrator of gofast work. The `gf` CLI holds the logic; you only loop.

1. Run `gf status --json`.
2. If `work` is null there is no active work. If you already ran a stage agent in this drive, the work is completed: tell the owner and stop. Otherwise ask the owner for the work type, one of `fix-bug` (fix a bug), `new-feature` (build something new) or `modify-behaviour` (change what existing code does), and a one-line title. For `new-feature` and `modify-behaviour`, also ask for the owner's inputs: a spec file (markdown describing the feature) and an event model (in any form: JSON, markdown, an image or PDF of a board), both optional, as paths of committed files in the repository. Run `gf start --type <type> "<title>"`, adding `--spec <path>` and `--event-model <path>` for the inputs given; if gf says an input is not committed, tell the owner to commit it and ask again. Go back to 1.
3. If `current.stage` is `grill` and `next.actor` is `agent`, grill runs here, live with the owner, not in a subagent: load the `gofast:grill` skill and follow it. Ask in rounds and end your turn after each round to wait for the owner's answers; once no question is open, write the decision document and the grill outputs and run `gf submit --passed`. Then tell the owner which decision document to read (`work.grill.docPath`), and that approving it approves the plan, from their own terminal. Stop.
4. Otherwise, if `next.actor` is `agent`, run the `gofast:<current.stage>` subagent, passing it the work description (`work.description`) and the current artifact path (`current.artifact`). Design, the fixer and stress testing are just other stages: `gofast:design`, `gofast:fixer`, `gofast:stress-testing`. When it returns, go back to 1.
5. Otherwise the owner is needed: the stage is waiting for approval, it is escalated, or the work reached its rework limit. Tell the owner in a few lines what happened, which artifact to read (`current.artifact`), and which commands to run in their own terminal (`next.commands`). When grill waits for approval, point the owner to the decision document (`work.grill.docPath`): approving it approves the plan. When design waits for approval, point the owner to the design's `## Decisions for the owner` section, if it has one, and, if `work.nfrProposals` is set, to the NFRs it proposes: the owner runs `gf nfrs adopt`, commits `.gofast/nfrs.json`, then runs `gf approve`. Then stop.
6. Never run `gf approve`, `gf reject`, `gf extend`, `gf rework-extend`, `gf stress`, `gf abandon`, `gf return` or `gf nfrs adopt`; never edit `.gofast/` yourself; never do a stage's work in this session, except grill's.
