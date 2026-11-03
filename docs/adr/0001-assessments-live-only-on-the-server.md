# Assessments live only on the history server and never reach the workflow

The repository is the source of truth for a work, and the history server holds an append-only copy that nothing comes back from. Assessments, the members' judgements of a stage visit or a work, are the first data written on the server rather than synced to it. We keep them there, so the server is their source of truth. gf never downloads them, and they never approve, reject, reopen or route anything: the owner's gates stay the only way a work moves. This keeps one direction of data flow, and it keeps the workflow's outcome decided only by the owner and the exit checks, not by whoever left the latest assessment.

## Considered Options

- **Let the fixer or later stages read the assessments of similar past visits.** This automates the skill-improvement loop. Rejected for now because it opens a second direction of data flow, makes the server a dependency of the workflow, and lets assessment text reach agents as instructions. It is the natural next step once assessments have proven useful by hand.
- **Store assessments in the repository.** Rejected because members who never clone a project can assess it, and an assessment of a work would have to be committed on that work's branch.

## Consequences

- Losing the server's database loses the assessments, but not the works. The server's backups now protect data that exists nowhere else.
