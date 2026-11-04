# gofast

gofast drives AI agents through a gated workflow, one stage at a time, and keeps a full history of
each piece of work so a team can inspect what each stage produced and which instructions produced it.

## Language

### The workflow

**Work**:
One piece of work of a given type (fix-bug, new-feature, …) moving along its path of stages.
_Avoid_: Task, job, unit of work

**Stage visit**:
One pass of a work through one stage; a work can visit a stage again after a rework or a return.
_Avoid_: Assignment (the code's name for it), step, run

**Attempt**:
One submission of a stage visit to its exit checks, which either fails or is accepted (or waits for the owner).
_Avoid_: Try, iteration

**Owner**:
The person who approves, rejects and unblocks a work; only the owner acts outside the agents.
_Avoid_: User, reviewer, admin

**Instruction version**:
The exact content of a skill or agent definition that instructed a stage visit, identified by its hash.
_Avoid_: Skill revision, release

### Inspecting the history

**Attempt diff**:
The code changes from the start of a stage visit to one of its attempts; a stage visit's diff is its last attempt's diff.
_Avoid_: Stage diff, patch

**History server**:
The shared, append-only copy of the works' histories that a team inspects; the repository stays the source of truth.
_Avoid_: Backend, dashboard

**Sync**:
Sending to the history server what it doesn't hold yet.
_Avoid_: Push, upload, export

### Who sees what

**Organisation**:
The group of people who share a history server's projects.
_Avoid_: Team, workspace, tenant

**Project**:
One repository's history on the history server, belonging to exactly one organisation; a member creates it in the viewer.
_Avoid_: Repo (on the server side), stream

**Project secret**:
The one secret every clone of a project syncs with, made by the server when the project is created or its secret rotated; it names the project, and besides syncing grants only reading the project's approved skill improvements.
_Avoid_: Sync token, API key

**Member**:
A person in an organisation, who can inspect every project in it.
_Avoid_: User, viewer, reader

### Judging the work

**Trouble**:
A moment in a work's history where something went wrong on its own record: a failed attempt, a rejection, an escalation, a rework, a return or an abandon.
_Avoid_: Error, incident, issue

**Assessment**:
A member's judgement of a stage visit or of a whole work: either good, or went wrong with the reason why. A member has at most one current assessment per stage visit or work.
_Avoid_: Review (that is a stage), rating, feedback, comment

**Skill improvement**:
A member's proposed change to one skill, written as text against the instruction version it improves; proposed, then approved (for one project or every project of the organisation), and possibly withdrawn.
_Avoid_: Feedback, suggestion, patch

**Approved** (skill improvement):
Accepted by any member for a project or for all projects; its text is fixed from then on.

**Applied** (skill improvement):
Written into a plugin directory's skill by the improvement agent and listed in the skill's record; the alternatives are declined (the owner reverted it) and not applied (a conflict or a refusal).

**Skill improved**:
The record that an improvement was applied, with the skill's versions before and after, or why it was not applied.

**Went wrong**:
The assessment that a stage visit or work did not do what it should have; it always carries the reason.
_Avoid_: Bad, failed (an attempt fails; a visit is assessed)
