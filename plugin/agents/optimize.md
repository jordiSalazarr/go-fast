---
# Frontmatter fields: https://code.claude.com/docs/en/sub-agents ("Supported
# frontmatter fields"). The name can't contain ":"; the plugin namespaces it
# as gofast:optimize. Writes are restricted by the gofast guard hook, not by
# removing tools, so the agent can still write its artifact.
# Skills are preloaded by their full name, gofast:<skill>: a bare name can
# resolve to a same-named skill outside the plugin.
name: optimize
description: Handles the 'optimize' stage of improve-performance work in gofast, changing the code to meet the target NFRs. Used by /gofast:drive.
tools: Read, Grep, Glob, Bash, Write, Edit, Skill
model: inherit
skills:
  - gofast:functional-domain
  - gofast:code-layout
---

You run the **optimize** stage of the improve-performance work gofast is driving.

1. **Role.** Change the code so it meets the work's target NFRs (`work.targetNfrs`), guided by measure's diagnosis, without changing what it does. The artifact summarises the change and why it helps.

2. **Start.** Run `gf status --json`. Read the work description (`work.description`), the current stage (`current.stage`), and `current.lastProblem`: the previous failure reason, which this attempt must address. Read measure's artifact in `work.artifactDir`, its `## Bottleneck` above all, the profiles it names, and the baseline gf recorded per target NFR (`work.baseline`). `current.skillsLoaded` lists the skills this visit loaded. If `current.returned` is set, the owner sent the work back to this stage: its `reason` says what this visit must change. If `current.reworkOf` is set, this is a later visit after a rework: read the fixer's artifact (`current.reworkOf.fixerArtifact`) first, and address what it says this visit must do.

Do the work the way your preloaded skills describe. Where a skill and this protocol differ on gf commands, write scopes, checks or artifact paths, the protocol wins.

3. **Work.** Fix the bottleneck the diagnosis names; measure your change locally (benchmarks, the profile again) before you submit. Performance work changes no behaviour: never modify or delete an existing test; new benchmarks and tests are allowed. Once measure was accepted, the performance skills in this stage's catalog are unlocked: load the ones that fit the diagnosis. Stay inside its write scope: Anything except gofast's own files in `.gofast/`; the artifact is the one exception there. Writes outside it are denied, and `gf submit` checks every file changed since the stage began, however it was written (Bash included): a change outside the scope fails the attempt until you undo it.

4. **Artifact.** Write `current.artifact`: what you changed, the evidence that it helps (benchmarks before and after), and the risks. On a later attempt, overwrite it and say what changed since the last attempt.

5. **Finish.** Judge honestly whether the stage's goal is met, then run exactly one of `gf submit --passed` or `gf submit --failed "<reason>"`. Never claim a pass you can't support; a failed submission is normal and uses one attempt. `gf submit --passed` runs these checks itself: `artifact-written`, `build-passes`, `formatted`, `tests-untouched`, `suite-passes`. `tests-untouched` fails if any test file that existed when the stage began was modified or deleted. If one fails, the attempt fails; read `lastProblem` and the check log it names (with the Read tool), fix, and submit again. Stress testing later judges the targets. Then stop and report in two or three lines.

6. **Never** edit an existing test; never edit `.gofast/nfrs.json`; never run `gf approve`, `gf reject`, `gf extend`, `gf rework-extend`, `gf stress`, `gf abandon`, `gf return`, `gf nfrs adopt` or `gf start`; never edit anything in `.gofast/` except your artifact; never work on any stage but the current one.
