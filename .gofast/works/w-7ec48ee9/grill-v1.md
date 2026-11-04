# Grill v1

Decision document: `docs/decisions/2026-11-04-w-7ec48ee9-skill-improvements.md`. ADR: `docs/adr/0004-approved-skill-improvements-flow-back-to-the-plugin.md` (supersedes ADR 0001 for skill improvements).

Summary: members write skill improvements on the history server; any member approves one for this project or all projects, or withdraws it. At startup only, gf reads its project's approved improvements with the project secret; an agent applies those missing from the skill directory's `.applied-improvements` record, only inside `$CLAUDE_PLUGIN_ROOT/skills/<name>/`, by intent, in approval order, refusing anything but a skill edit; the change stays uncommitted and is shown as a diff. Each outcome (applied, declined, not applied) is recorded as Skill improved and synced; the viewer shows it per improvement and per stage visit. Own skills only. 23 decisions, 17 acceptance scenarios, no open questions.
