# Deploying the history server

`gofast-server` is one static binary and one SQLite file. Pick one way to
run it, and put TLS in front of it.

## With Docker and Caddy

```sh
cd deploy
cp server.example.json server.json
# Make the project's secret and each member's token, and put their
# SHA-256s in server.json:
PROJECT_SECRET=$(openssl rand -hex 32); MEMBER_TOKEN=$(openssl rand -hex 32)
printf %s "$PROJECT_SECRET" | shasum -a 256
printf %s "$MEMBER_TOKEN" | shasum -a 256
HISTORY_DOMAIN=history.example.com docker compose up -d --build
```

Caddy gets the TLS certificate for `HISTORY_DOMAIN` by itself (its DNS must
point at the machine). The database lives in the `history` volume.

**Backups:** `docker compose exec gofast-server /gofast-server -config
/config/server.json -backup /data/backup-$(date -u +%Y%m%d).db` writes a
consistent copy while the server runs; copy it off the machine. Schedule it
with cron on the host.

**The backups hold the only copy of the assessments.** Everything else on
the server is a copy of what the repositories hold, and a lost database can
be synced again; the members' assessments are written on the server and
exist nowhere else (`docs/adr/0001-assessments-live-only-on-the-server.md`).
Keep the backups off the machine, and try restoring one.

## On Railway

Railway builds `deploy/Dockerfile`, gives the service a domain with TLS,
and keeps the database on a volume. From the repository's root:

```sh
railway init --name gofast-history
railway add --service gofast-server \
  --variables "RAILWAY_DOCKERFILE_PATH=deploy/Dockerfile" \
  --variables "RAILWAY_RUN_UID=0" \
  --variables "GOFAST_SERVER_CONFIG=$(cat server.json)"
railway volume --service gofast-server add --mount-path /data
railway up --service gofast-server --ci
railway domain --service gofast-server
```

- `GOFAST_SERVER_CONFIG` is the whole `server.json`, with `"db":
  "/data/history.db"` and no `listen` (the server listens on `$PORT`, or
  `:8080`, which Railway's domain finds). It holds only hashes.
- `RAILWAY_RUN_UID=0`: the image runs as a non-root user, and Railway
  mounts volumes for root.
- Turn on the volume's **backups** in the dashboard (the service's Backups
  tab): daily is kept 6 days, weekly a month, monthly three months. They
  are the assessments' only copy.

Run one replica only: the database is one SQLite file on one volume.
Changing `GOFAST_SERVER_CONFIG` (a new member, a rotated secret) with
`railway variables --service gofast-server --set "GOFAST_SERVER_CONFIG=…"`
redeploys the service, with a few seconds' downtime. `railway up` deploys
the working tree as it is, committed or not.

## With systemd

Copy `gofast-server` to `/usr/local/bin/`, write `/etc/gofast/server.json`
with `"db": "/var/lib/gofast/history.db"`, then install the units:

```sh
sudo cp gofast-server.service gofast-backup.service gofast-backup.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now gofast-server gofast-backup.timer
```

The timer backs the database up daily into `/var/lib/gofast-backups/`,
keeping the last 14 copies. Put Caddy (see `Caddyfile`, with
`reverse_proxy localhost:8080`), nginx or your load balancer in front for
TLS.

## Then

- Give everyone who syncs the project the **project secret**
  (`GOFAST_PROJECT_SECRET` in their shell). It syncs and does nothing else.
- Give each member their own **member token**. They sign in with it at
  `https://<domain>/` to inspect and assess every project of their
  organisation. A member's token can't sync.
- **When a member leaves**, remove their hash from `server.json` and
  restart. If they held a project secret, make a new secret, put its hash in
  `server.json`, restart, and give it to everyone who syncs that project:
  there is one secret per project (`docs/adr/0002-one-shared-secret-per-project.md`).
