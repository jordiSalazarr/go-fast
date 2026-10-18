# Deploying the history server

`gofast-server` is one static binary and one SQLite file. Pick one way to
run it, and put TLS in front of it.

## With Docker and Caddy

```sh
cd deploy
cp server.example.json server.json
# Make the tokens, and put their SHA-256s in server.json:
SYNC_TOKEN=$(openssl rand -hex 32); READ_TOKEN=$(openssl rand -hex 32)
printf %s "$SYNC_TOKEN" | shasum -a 256
printf %s "$READ_TOKEN" | shasum -a 256
HISTORY_DOMAIN=history.example.com docker compose up -d --build
```

Caddy gets the TLS certificate for `HISTORY_DOMAIN` by itself (its DNS must
point at the machine). The database lives in the `history` volume.

**Backups:** `docker compose exec gofast-server /gofast-server -config
/config/server.json -backup /data/backup-$(date -u +%Y%m%d).db` writes a
consistent copy while the server runs; copy it off the machine. Schedule it
with cron on the host.

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

- Give `gf sync` the **sync token** (`GOFAST_SYNC_TOKEN` in your shell).
- Open `https://<domain>/` and sign in with the project and a **read token**
  to browse the history. Read tokens can't write.
- To revoke a token, remove its hash from `server.json` and restart.
