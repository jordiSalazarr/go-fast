# Members create projects, and the server makes their secrets

A member creates a project of their organisation in the viewer, and the server makes its project secret, shows it once and keeps only its hash. So projects, like assessments, are data whose source of truth is the server, and the configuration lists only organisations and their members. A member rotates a project's secret the same way, which retires the old one at once. gf keeps the secret in `.gofast/sync.secret`, which `.gofast/.gitignore` lists, so it never leaves the machine through git; `$GOFAST_PROJECT_SECRET` still wins when set. We chose this so starting to sync a repository needs no edit to the server's configuration and no restart, and no person ever picks a secret.

## Considered Options

- **Projects and secret hashes in the configuration.** Every new project or rotation meant generating a secret by hand, editing the configuration and redeploying. Rejected as the everyday path.
- **The secret committed in `.gofast/sync.json`.** Every clone would sync with no setup, but anyone who can read the repository could sync to the project. Rejected: each clone saves the secret once instead (`gf sync secret`).

## Consequences

- Losing the server's database loses which projects exist and their secrets, besides the assessments: every repository then needs its project created again and a new secret. The backups protect both.
- Any member can rotate any project of their organisation, and a rotation stops every clone syncing until it saves the new secret.
- No agent may read or write `.gofast/sync.secret`: gf's guard denies it to every tool.
