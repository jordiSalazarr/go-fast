# One shared secret per project for syncing, personal tokens for members

Every clone of a project syncs with the same project secret. The server keeps only its hash, and the hash names the project. The secret can sync and do nothing else. Reading and assessing take a member's personal token, so they are tied to a person, while syncing is not. We chose this because syncing doesn't need to prove who someone is: every synced event already carries its actor (the git identity of the owner who made it), and the server refuses a work's stream when it conflicts with what it holds. Personal sync tokens would add one more credential per person and project to issue and revoke, without a question it answers today.

## Considered Options

- **A sync token per person** (`tokenSha256s`). It lets you revoke one person's ability to sync, and it would tie each event to the token that sent it, so a holder of the secret couldn't sync events claiming another actor without that showing. Rejected for v1: the secret is held only by people trusted to own works, and leaks are handled by rotating it.

## Consequences

- A leaked or departing teammate's secret means rotating the project's secret for everyone who syncs it.
- An event's actor is what the clone said, not something the server verified.
