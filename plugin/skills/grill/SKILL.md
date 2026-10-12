---
# Frontmatter fields: https://code.claude.com/docs/en/skills ("Frontmatter
# reference"). In plugin "gofast" this skill is gofast:grill. The drive
# loads it when the work is on the 'grill' stage: grill runs in the main
# session, live with the owner, never in a subagent.
name: grill
description: The protocol of gofast's 'grill' stage, a live session with the owner that settles every open question before discovery. Loaded by /gofast:drive.
user-invocable: false
---

You run the **grill** stage of the work gofast is driving, in this session, with the owner.

1. **Role.** Make sure everything about the work is clear before anyone looks at the code: ask the owner until no question is open.

2. **Start.** Run `gf status --json`. Read the work description (`work.description`) and `current.lastProblem`: the previous failure reason or the owner's rejection feedback, which this visit must address. If `current.returned` is set, the owner sent the work back to grill: its `reason` says what this visit must change. If `current.reworkOf` is set, read the fixer's artifact (`current.reworkOf.fixerArtifact`) first.

3. **Rounds.** Ask in rounds, and wait for the owner's answers before the next round.

4. **Artifact.** Write `current.artifact`. Stay inside the stage's write scope: Only the artifacts.

5. **Finish.** Run `gf submit --passed` or `gf submit --failed "<reason>"`. `gf submit --passed` runs these checks itself: `artifact-written`. If one fails, read `lastProblem`, fix, and submit again. Then tell the owner which document to read, and to approve from their own terminal.

6. **Never** run `gf approve`, `gf reject`, `gf extend`, `gf rework-extend`, `gf stress`, `gf abandon`, `gf return` or `gf nfrs adopt`; never edit anything in `.gofast/` except your artifacts; never work on any stage but the current one.
