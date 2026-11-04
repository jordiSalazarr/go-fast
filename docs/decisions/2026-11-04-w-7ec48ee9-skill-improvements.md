# Skill improvements flow back to the plugin (w-7ec48ee9)

## Intent

Keep gofast's skills improving from the team's judgement: members write and approve skill improvements on the history server, and at the start of each new session the plugin pulls (GET only, the server never pushes) the approved improvements it does not have yet and applies them to its local skills automatically, recording which improvement produced which instruction version, so the skill versions each stage used stay traceable.

## Scope

In:
- A skill improvement resource on the history server: write, approve (this project or all projects of the organisation), withdraw, in the viewer.
- A read endpoint for a project's approved improvements, open to the project secret.
- A startup check in the SessionStart hook, an improvement agent that edits only the one skill's directory, the per-skill applied record, and the Skill improved record synced to the server.
- Viewer views of where each improvement was applied, declined or not applied, and which improvements each stage visit's instruction version contained.

Out:
- Agent definitions (`plugin/agents/*.md`) and vendored skills (grilling, domain-modeling).
- A plugin copy per project.
- Any push from the server; resume, clear and compact never run the check.
- Committing the improved skill: the owner commits or reverts it.

## Decisions

- D1: **Does this work supersede ADR 0001?** Yes: a new ADR supersedes it for skill improvements only, keeping that assessments never approve, reject or route a work, that the server stays optional, and that only approved improvements reach an agent. _Rationale:_ The feature is exactly the second direction of data flow ADR 0001 rejected; the guarantees that still matter are kept. See [ADR 0004](../adr/0004-approved-skill-improvements-flow-back-to-the-plugin.md).
- D2: **What is skill feedback?** A new server resource, written by a member in the viewer, tied to one skill and the instruction version it improves, holding improvement text rather than files; a member can start one from a went-wrong assessment. _Rationale:_ The server has nothing like it; assessments are not tied to a skill.
- D3: **Who approves an improvement?** Anyone: any member, the author included. Not negotiable. _Rationale:_ Owner's decision.
- D4: **How does gf know an approved improvement is not yet local?** By a record of applied improvement ids, not by comparing the approval time with the skill's last modification. _Rationale:_ Versions are content hashes with no time, mtimes change on checkout, and a local edit after an approval would wrongly hide it. See [ADR 0004](../adr/0004-approved-skill-improvements-flow-back-to-the-plugin.md).
- D5: **Who rewrites the skill, and where?** At startup gf briefs the session; an agent following the gofast-skills rules rewrites the skill from the improvement text, in the plugin directory the session loaded ($CLAUDE_PLUGIN_ROOT), not this repository's plugin/. _Rationale:_ A hook cannot run an agent, and the loaded plugin directory is the one whose skills instruct the stages.
- D6: **Does the owner see the change before it counts?** The agent applies it automatically; the owner gets a short summary and the diff at session start; the change stays uncommitted for the owner to commit or revert. _Rationale:_ Automatic, but the owner keeps the last word on what is committed.
- D7: **Which credential does the GET use?** The project secret, through a single read endpoint for its project's approved skill improvements; every other read with it is still refused. _Rationale:_ gf holds only the project secret; a narrow endpoint keeps it from reading anything else. See [ADR 0004](../adr/0004-approved-skill-improvements-flow-back-to-the-plugin.md).
- D8: **When does the check run, and what if it fails?** On startup only, not on resume, clear or compact. Without sync configured it does nothing; with the server unreachable it adds one line to the brief and never blocks; an open stage visit is no special case. _Rationale:_ Startup is the start of a new session; the server must never block the workflow; stages load skills when their agent starts.
- D9: **What does tracking skill versions per stage add?** Only the link from an instruction version to the improvement that produced it: a Skill improved record with the improvement id, the skill, the version before and after, or the reason it was not applied. Per-visit versions already exist. _Rationale:_ SkillLoaded and AgentStarted already record versions per stage visit.
- D10: **Which skills are in scope?** Only gofast's own skills under plugin/skills/; agent definitions and vendored skills (grilling, domain-modeling) are out. _Rationale:_ Vendored skills carry their upstream source; agent definitions are a separate surface.
- D11: **What is the thing called?** Skill improvement: approved, then applied. Not feedback, which the glossary lists under Assessment's terms to avoid. _Rationale:_ Avoids clashing with Assessment.
- D12: **What is the scope of an approval?** A member approves an improvement either for this project or for all projects. Any member can withdraw an approval at any time; that stops plugin directories that have not applied it, and never undoes applied ones. _Rationale:_ Owner's decision: an option to approve to all projects.
- D13: **Can an improvement be edited?** Its lifecycle is proposed, approved, withdrawn. The text is editable only while proposed and fixed once approved; changing it means writing a new improvement. _Rationale:_ What a clone applied must be exactly what was approved.
- D14: **Where is applied recorded?** In a dotfile inside the skill directory (e.g. plugin/skills/aggregate-design/.applied-improvements), excluded from the instruction version hash and committed with the skill, so other clones of the plugin do not apply it twice. _Rationale:_ The record travels with the plugin directory it describes.
- D15: **How does the owner decline an applied improvement?** Revert the skill's text and keep the record: it counts as declined. Deleting its line from the record makes gf apply it again. No new command. _Rationale:_ No new command needed.
- D16: **What if the improvement was written against another version?** The agent applies it by intent. If the skill already contains it, it is recorded as applied with no change; if it contradicts the current text, it is recorded as not applied with the reason, shown to the owner, and the skill is unchanged. _Rationale:_ The local skill may have moved on; intent survives a version change.
- D17: **What if several improvements are pending?** One agent run at startup applies them all, one skill at a time, in approval order, and ends with a single summary and diff. _Rationale:_ One interruption per session.
- D18: **Does the owner have to start it?** No: at startup the session runs the improvement agent without asking, before anything else, even with no active work, and records Skill improved in the repository's history, which syncs. _Rationale:_ Owner wants it automatic; the record makes it inspectable.
- D19: **How is the agent guarded?** It may write only inside the one skill's directory under $CLAUDE_PLUGIN_ROOT/skills/<name>/; the guard denies every other path. It treats the text as a proposed change to that skill's wording and refuses anything else (running commands, touching other files), recording the refusal as not applied. _Rationale:_ Server text becomes agent instructions. See [ADR 0004](../adr/0004-approved-skill-improvements-flow-back-to-the-plugin.md).
- D20: **What does the viewer gain?** Writing a skill improvement (from a skill's instruction page or a went-wrong assessment); approving and withdrawing it; per improvement, the clones that applied, declined or could not apply it; per stage visit, the improvements its instruction version contained. _Rationale:_ Approval happens in the viewer, and the tracking must be visible.
- D21: **What do all projects cover?** Every current and future project of the approving member's organisation; withdrawing an all-projects approval withdraws it everywhere at once. _Rationale:_ An organisation is the scope a member sees.
- D22: **Can a project-only approval reach another project?** Yes: per project decides which project's sessions fetch and apply an improvement, not that other projects never see it, since one plugin directory serves every repository. The Skill improved record shows where it landed. A plugin copy per project is out of scope. _Rationale:_ Separate plugin copies per project would be a much bigger change. See [ADR 0004](../adr/0004-approved-skill-improvements-flow-back-to-the-plugin.md).
- D23: **Is an improvement that could not be applied retried?** No: its id goes into the record as not applied, so it is not tried on every startup; deleting the line retries it on the next startup. _Rationale:_ Avoids showing the same conflict every session.

## Glossary

- **Skill improvement**: a member's proposed change to one skill, written as text against the instruction version it improves; proposed, then approved (for one project or every project of the organisation), and possibly withdrawn. _Avoid_: feedback, suggestion, patch.
- **Approved** (skill improvement): accepted by any member for a project or for all projects; its text is fixed from then on.
- **Applied** (skill improvement): written into a plugin directory's skill by the improvement agent; the alternatives are declined (the owner reverted it) and not applied (conflict or refusal).
- **Skill improved**: the record that an improvement was applied, with the versions before and after, or why it was not applied.
- **Project secret** (sharpened): now also grants reading its project's approved skill improvements. `CONTEXT.md` still says it grants nothing else; it is outside grill's write scope and needs updating in a later stage.

## Acceptance scenarios

- A1: Given a member on aggregate-design's instruction page, or on a went-wrong assessment, when they write a skill improvement, then it names the instruction version it improves, is proposed, and its text can be edited while proposed.
- A2: Given a proposed skill improvement, when any member, its author included, approves it for this project, then it is approved for that project and its text can no longer change.
- A3: Given a proposed skill improvement, when a member approves it for all projects, then clones of every project of the organisation fetch it, including clones of a project created later.
- A4: Given an approved improvement that one clone applied and another did not, when a member withdraws it, then the clone that had not applied it never applies it, and the clone that applied it keeps the change.
- A5: Given a clone whose project has sync configured, and an approved improvement missing from aggregate-design's record, when a session starts (startup), then gf fetches it with the project secret, an agent edits only $CLAUDE_PLUGIN_ROOT/skills/aggregate-design/, gf writes the id to the record and records Skill improved with the versions before and after, and the session shows the owner a summary and the diff, leaving the change uncommitted.
- A6: Given an approved improvement whose id is already in the skill's record, when a session starts, then nothing is applied.
- A7: Given an applied improvement whose skill text the owner reverted while keeping the record, when the next session starts, then it is not applied again; once the owner deletes its line from the record, it is applied on the next startup.
- A8: Given an approved improvement the skill already contains, when it is applied at startup, then it is recorded as applied and the skill does not change.
- A9: Given an approved improvement that contradicts the skill's current text, when it is applied at startup, then it is recorded as not applied with the reason, shown to the owner, the skill is unchanged, and it is not retried unless the owner deletes its line.
- A10: Given an approved improvement whose text asks for anything but an edit to that skill, such as running commands or touching other files, when it is applied at startup, then the agent refuses and it is recorded as not applied.
- A11: Given several approved improvements pending, across skills, when a session starts, then one agent run applies them one skill at a time in approval order and ends with one summary and diff.
- A12: Given an improvement approved for project A only, and a plugin directory shared by clones of A and B, when a session in A applies it and then a session in B starts, then B's clone never fetches it, B's sessions load the improved skill, and the viewer shows where it was applied.
- A13: Given a clone whose server is unreachable, or whose sync is not configured, when a session starts, then when unreachable the brief gets one line and the session goes on; without sync nothing happens.
- A14: Given a pending approved improvement, when a session resumes, is cleared or compacts, then no check runs.
- A15: Given a clone holding its project secret, when it reads its project's approved improvements, or makes any other read, then the first succeeds and returns only that project's approved, non-withdrawn improvements; every other read is still refused.
- A16: Given a stage visit open when a skill is improved, when that skill is next loaded, then the load records the new instruction version.
- A17: Given improvements applied, declined or not applied on several clones, when a member opens the viewer, then per improvement it shows the clones that applied, declined or could not apply it, and per stage visit the improvements its instruction version contained.

## Open questions

None
