# Approved skill improvements flow back from the history server to the plugin

Supersedes, for skill improvements only, the "never reach the workflow" rule of [ADR 0001](0001-assessments-live-only-on-the-server.md). Members write a skill improvement on the history server and any member approves it, for one project or for every project of the organisation. At session startup gf asks the server, with the project secret, for the approved improvements of its project, and an agent applies those the plugin directory's record does not list yet to the skill under `$CLAUDE_PLUGIN_ROOT`. The flow stays a pull: the server never pushes. ADR 0001's other guarantees hold: assessments still never approve, reject, reopen or route a work; the server stays optional, so gf works the same when it is unreachable; and only approved improvements ever reach an agent, which may edit only the one skill's directory and refuses anything else.

Whether an improvement is "already here" is decided by a record of applied improvement ids kept as a dotfile in the skill directory, not by comparing the approval time with the skill's modification time: a skill's version is a content hash with no time, file modification times change on every checkout, and a local edit made after an approval would wrongly hide it.

## Considered Options

- **Keep ADR 0001 and improve skills by hand from the viewer.** Rejected: the team wants approved improvements to reach every clone without each owner copying them.
- **Compare the approval time with the skill's last modification.** Rejected for the reasons above.
- **Give gf a member token for the read.** Rejected: the project secret already names the project, and one narrow read endpoint keeps it from reading anything else.
- **A plugin copy per project, so a project-only approval never reaches another project.** Rejected as a much bigger change: one plugin directory serves every repository, so "this project" decides which sessions fetch an improvement, and the record shows where it landed.

## Consequences

- The server is now a source of instructions to agents. Its approvals are the gate; a member who can approve can change what every clone's agents are told.
- The project secret can read one thing: its project's approved skill improvements.
- An improved skill stays uncommitted in the plugin directory until the owner commits or reverts it.
