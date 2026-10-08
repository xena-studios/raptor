# Deploying the Panel

How to stand up `raptorpanel.net` from nothing: two servers, Cloudflare, 1Password, backups, and the static sites. The files it uses are in [`deploy/`](../deploy). The steps that run on the servers are rehearsed end to end on one machine (`task deploy:rehearse`, [below](#rehearsal)), except where a step says otherwise. The Cloudflare and provider steps can't be rehearsed; each ends with what to check.

The why behind these choices is in [PANEL.md](PANEL.md#hosting), [RELIABILITY.md](RELIABILITY.md#panel), and [DECISIONS.md](DECISIONS.md) (#200–#206).

## What you're building

```
                       Cloudflare (proxy, WAF, Turnstile)
browsers ──https──▶  app.raptorpanel.net     (Worker: web/dist)
                     verify.raptorpanel.net  (Worker: web/verify)
                     get.raptorpanel.net     (redirect to the latest install.sh)
browsers, nodes ──▶  api.raptorpanel.net ──client cert──▶ server #1
                                                         Caddy :443
                                                         ├─ panel-a ┐
                                                         └─ panel-b ┴─▶ Postgres (primary)
                                                                          │  WAL, backups ──▶ object storage
                                                         private network  │                  (another provider)
                                                                          ▼
                                                         server #2: Postgres (streaming replica)
                                                         + the same Caddy and Panels, stopped,
                                                           for a failover
nodes' hostnames:    n-<id>.raptornodes.net  (DNS only, written by the Panel)
```

- **Server #1** runs everything: Caddy (TLS from Cloudflare's Origin CA, and only Cloudflare's client certificate gets in), two Panel instances (so a deploy never takes the API down), and Postgres with pgBackRest.
- **Server #2** runs a streaming replica. If server #1 dies, you promote it and start the Panels there ([failover](#failover)): minutes, not the hours a restore from the archive would take.
- **Backups** go to object storage at a different provider, encrypted before they leave the server. Every WAL segment is archived within a minute, there's a backup every night, and a restore is tested every month, automatically.
- **Secrets** live in 1Password and are written to memory (`/run/raptor`) at boot. Nothing secret is on disk or in the repo.
- **The web app** is static, served by Cloudflare Workers (static assets, no code), deployed with a token that can do nothing else.

## Before you start

You need:

| What | Notes |
|---|---|
| Two Linux servers | Same provider and region, **different datacenters**, joined by a **private network**. Debian 13 (or 12, or Ubuntu 24.04), x86-64 or arm64. 4 vCPU, 8 GB RAM, 80 GB NVMe each is plenty at launch; the Panel itself is small, Postgres gets the rest. Hetzner Cloud (two locations in one network zone, e.g. `fsn1` and `nbg1`, with a Cloud Network) is a cheap fit; any provider with private networking works. |
| Object storage | S3-compatible, at a **different provider** from the servers (a provider outage or account problem mustn't take the database and its backups together). Backblaze B2 or Wasabi are fine. One bucket, private, versioning off (pgBackRest manages its own retention), and an access key that can only use that bucket. |
| Cloudflare | `raptorpanel.net` and `raptornodes.net` as zones. The free plan works for both to start; `raptornodes.net` needs a plan with enough DNS records once you pass the free plan's limit (one record per node). |
| 1Password | A vault called **Raptor production**, and a service account that can read it (and nothing else). |
| Resend, Turnstile, OAuth apps | Already made: you have the keys. They go in the vault, below. |
| A machine with Go, Docker, and the `op` CLI | Your Mac. For making keys and running the rehearsal. |

Pick the two servers' private addresses now; this guide calls them `10.0.0.2` (server #1) and `10.0.0.3` (server #2).

## Secrets

Everything the servers need comes from the **Raptor production** vault. Make these items (field names matter: the templates in [`deploy/secrets/`](../deploy/secrets) read them):

| Item | Fields | How to make it |
|---|---|---|
| `Panel signing key` | `key` | `go run ./cmd/panel keygen /tmp/signing.key`, paste the file's one line, then `rm -P /tmp/signing.key`. Also save the printed public key in the item's notes. **Nodes pin this key when they link: losing it means relinking every node. Never rotate it casually.** |
| `Panel data key` | `key` | Same, a separate run. It encrypts TOTP secrets: losing it turns off everyone's authenticator app. |
| `Postgres` | `superuser_password`, `panel_password`, `replicator_password` | Three random passwords: `openssl rand -hex 32` each. Hex, so they're safe in a database URL. |
| `Backup storage` | `bucket`, `endpoint`, `region`, `access_key_id`, `secret_access_key`, `encryption_passphrase` | From the storage provider (endpoint without `https://`, e.g. `s3.us-west-004.backblazeb2.com`). The passphrase: `openssl rand -base64 48`. **Without it the backups can't be read: it's in 1Password and only there, so make sure the vault itself is backed up (1Password's emergency kit).** |
| `Resend` | `api_key` | The production key: sending access only, for `mail.raptorpanel.net`. |
| `Turnstile` | `secret_key` | The production widget's secret. |
| `GitHub OAuth` | `client_id`, `client_secret` | The production app (callback `https://api.raptorpanel.net/oauth/github/callback`). |
| `Google OAuth`, `Discord OAuth` | `client_id`, `client_secret` | When you've made them. **Until then, delete their lines from `deploy/secrets/panel.env.tpl` on the servers**: `op inject` fails on a missing item. A provider is offered once both its values are set. |
| `Cloudflare raptornodes.net DNS` | `token`, `zone_id` | [Below](#raptornodesnet). |
| `Support bundles storage` | `bucket`, `endpoint`, `region`, `access_key_id`, `secret_access_key` | [Below](#support-bundles). |
| `Grafana Cloud OTLP` | `endpoint`, `headers` | [Below](#monitoring). |

Rotate anything that was ever pasted in a chat or a terminal history.

Then the service account: 1Password → Developer → Service accounts → new, **read-only** access to **Raptor production** only. Its token goes on each server as `/etc/raptor/op-token` (below) and nowhere else: put a copy in the vault itself (item `Service account`) so you can reinstall a server.

## Cloudflare

### raptorpanel.net

1. **SSL/TLS → Overview:** Full (strict). **Edge Certificates:** Always Use HTTPS on, minimum TLS 1.2, TLS 1.3 on, HSTS on (max-age 6 months to start, include subdomains; add preload only once everything is on HTTPS for good).
2. **DNS:**

   | Name | Type | Content | Proxy |
   |---|---|---|---|
   | `api` | A | server #1's public IPv4 | Proxied |
   | `api` | AAAA | server #1's public IPv6 (if it has one) | Proxied |
   | `app`, `verify` | | made by Workers when you attach the domains, [below](#static-sites) | Proxied |
   | `get` | AAAA | `100::` | Proxied (a placeholder: the redirect rule answers) |

   The Resend records for `mail` and the Email Routing records are already there.
3. **Origin certificate** (for Caddy): SSL/TLS → Origin Server → Create certificate: ECDSA, hostname `api.raptorpanel.net`, 15 years. Save the certificate as `origin.pem` and the key as `origin-key.pem`, and put both in the vault (item `Origin certificate`) too.
4. **Authenticated Origin Pulls**, so only Cloudflare can reach Caddy. Use **per-hostname** pulls with your own certificate, not the zone-level one: the zone-level certificate is shared by every Cloudflare customer, so anyone could point their own zone at your server's IP and get through. On your Mac:

   ```bash
   mkdir -p /tmp/aop && cd /tmp/aop
   openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 \
     -subj "/CN=Raptor origin pull CA" -keyout ca-key.pem -out origin-pull-ca.pem
   openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=cloudflare" \
     -keyout client-key.pem -out client.csr
   openssl x509 -req -in client.csr -CA origin-pull-ca.pem -CAkey ca-key.pem -CAcreateserial \
     -days 3650 -out client.pem
   ```

   Upload the client certificate and turn it on for `api` (with a short-lived API token that has Zone → SSL and Certificates → Edit on `raptorpanel.net`; delete the token afterwards):

   ```bash
   ZONE=<raptorpanel.net zone ID>; CF_TOKEN=<token>
   cert_id=$(jq -n --rawfile c client.pem --rawfile k client-key.pem '{certificate:$c,private_key:$k}' |
     curl -fsS -X POST "https://api.cloudflare.com/client/v4/zones/$ZONE/origin_tls_client_auth/hostnames/certificates" \
       -H "Authorization: Bearer $CF_TOKEN" -H 'Content-Type: application/json' --data @- | jq -r .result.id)
   curl -fsS -X PUT "https://api.cloudflare.com/client/v4/zones/$ZONE/origin_tls_client_auth/hostnames" \
     -H "Authorization: Bearer $CF_TOKEN" -H 'Content-Type: application/json' \
     --data "{\"config\":[{\"hostname\":\"api.raptorpanel.net\",\"cert_id\":\"$cert_id\",\"enabled\":true}]}"
   ```

   `origin-pull-ca.pem` goes on the servers; put the CA key and client key in the vault (item `Origin pull CA`) and delete `/tmp/aop`. The certificates last 10 years; set a reminder for year 9.
5. **Security:**
   - **Bot Fight Mode off.** Wings is a bot, and on the free plan Bot Fight Mode can't be skipped for a path: it would challenge node connections, which can't solve challenges.
   - WAF → Custom rules → "API: no challenges": when `http.host eq "api.raptorpanel.net"`, **Skip** the remaining custom rules, rate limiting rules, Super Bot Fight Mode, and Browser Integrity Check. The Panel has its own rate limits and Turnstile; a Cloudflare challenge page in the middle of an API call or a node's WebSocket only breaks it.
   - Security level: medium or lower. Under-attack mode breaks the API and nodes: don't turn it on for `api`.
6. **Network:** WebSockets on (the default). **Caching → Cache Rules:** a rule bypassing the cache for `api.raptorpanel.net`.
7. **Rules → Redirect Rules:** `get.raptorpanel.net` → `https://github.com/xena-studios/raptor/releases/latest/download/install.sh`, status 302. The script checks the binary against the SHA-256 it was generated with (`task release:sign`), so where it's served from isn't a trust boundary; GitHub's "latest" skips pre-releases.

**Check:** after the servers are up, `curl -fsS -o /dev/null -w '%{http_code}\n' https://api.raptorpanel.net/healthz` prints `200`, and `curl -k --resolve api.raptorpanel.net:443:<server #1 IP> https://api.raptorpanel.net/healthz` from anywhere but Cloudflare fails (the firewall drops it; with the firewall off, Caddy wants a client certificate).

### raptornodes.net

1. Every record is **DNS only** (grey cloud). Node hostnames point straight at nodes.
2. Apex: a Redirect Rule `raptornodes.net/*` → `https://raptorpanel.net`, 301 (with a proxied placeholder record `@ AAAA 100::` for it to answer on).
3. A token for the Panel: My Profile → API Tokens → Custom: permission **Zone → DNS → Edit**, zone **raptornodes.net only**, client IP filtering **server #1's and server #2's public IPs**. The Panel holds this token and no other Cloudflare token; it can't touch `raptorpanel.net`. Put it and the zone ID in the vault (`Cloudflare raptornodes.net DNS`).
4. Later, before launch: submit `raptornodes.net` to the [Public Suffix List](https://publicsuffix.org) ([ARCHITECTURE.md](ARCHITECTURE.md#node-dns)).

### Accounts

Hardware-key 2FA (no SMS, no backup codes left lying around) on Cloudflare, the registrar, 1Password, GitHub, Resend, the server provider, and the storage provider. Registrar lock on both domains. Whoever controls DNS or the static host controls the code users run.

## Servers

### Both servers

As root, on a fresh install:

```bash
apt-get update && apt-get -y upgrade
apt-get install -y ca-certificates curl git gnupg ufw unattended-upgrades jq
dpkg-reconfigure -plow unattended-upgrades

# Docker, from Docker's repository.
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update && apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# The 1Password CLI.
curl -sS https://downloads.1password.com/linux/keys/1password.asc | gpg --dearmor -o /usr/share/keyrings/1password-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/1password-archive-keyring.gpg] https://downloads.1password.com/linux/debian/$(dpkg --print-architecture) stable main" \
  > /etc/apt/sources.list.d/1password.list
apt-get update && apt-get install -y 1password-cli

# The deploy files.
git clone https://github.com/xena-studios/raptor.git /opt/raptor
install -d -m 0700 /etc/raptor /etc/raptor/tls
```

(On Ubuntu, use `ubuntu` in Docker's repository URL.)

SSH: keys only (`PasswordAuthentication no`, `PermitRootLogin prohibit-password` in `/etc/ssh/sshd_config`), and your provider's firewall, if it has one, open for 22 and 443 only.

Then the files that aren't in the repo:

```bash
# The 1Password service account token: root only.
install -m 0600 /dev/stdin /etc/raptor/op-token <<< '<the service account token>'
# Caddy's certificate, its key, and the origin pull CA (from Cloudflare, above).
install -m 0644 origin.pem origin-pull-ca.pem /etc/raptor/tls/
install -m 0600 origin-key.pem /etc/raptor/tls/
# The container runs as root, so 0600 root is readable by it and nobody else.
```

Check the secrets come through:

```bash
/opt/raptor/deploy/scripts/secrets.sh && ls -l /run/raptor /run/raptor/keys
```

The firewall, naming the other server's private address (on server #1, `10.0.0.3`; on server #2, `10.0.0.2`):

```bash
/opt/raptor/deploy/scripts/firewall.sh 10.0.0.3
```

It allows SSH, 443 from Cloudflare's addresses only, and Postgres from the other server only. Docker publishes ports around ufw, so 443 is filtered in Docker's own chain (`DOCKER-USER`) and Postgres is only bound to the private address. Rerun it now and then: Cloudflare's address list changes rarely, but it changes.

The boot unit, timers, and the server's settings:

```bash
cp /opt/raptor/deploy/systemd/* /etc/systemd/system/ && systemctl daemon-reload
systemctl enable raptor-stack.service
```

### Server #1

Its settings, read by Compose on every run:

```bash
cat > /opt/raptor/deploy/primary/.env <<'EOF'
PRIVATE_IP=10.0.0.2
EOF
```

Postgres first, then the backup repository:

```bash
cd /opt/raptor/deploy/primary
../scripts/secrets.sh
docker compose up -d --wait postgres
docker compose exec -u postgres postgres pgbackrest --stanza=raptor stanza-create
docker compose exec -u postgres postgres pgbackrest --stanza=raptor check
```

`check` archives a WAL segment and confirms it arrived: if it fails, the bucket settings or keys are wrong. The first start also creates the `panel` and `replicator` roles ([`initdb/10-roles.sh`](../deploy/primary/initdb/10-roles.sh)); the Panel isn't a superuser.

Then the Panel, at a released version (the release workflow publishes `ghcr.io/xena-studios/raptor-panel:<tag>`):

```bash
../scripts/deploy.sh v0.x.y
docker compose up -d caddy
```

`deploy.sh` runs the migrations, starts both Panels, and records the version in `.env` so a reboot starts the same one. GHCR makes new packages private: after the first release, set `raptor-panel` and `raptor-postgres` to public (the organization's Packages → each package → Package settings), or `docker login ghcr.io` on both servers with a token that can only read packages.

The first full backup, and the timers:

```bash
../scripts/backup.sh full
systemctl enable --now raptor-backup.timer raptor-restore-test.timer
../scripts/restore-test.sh    # once now, to see it work: about a minute
```

**Check:** `https://api.raptorpanel.net/healthz` answers 200; signing in on `app.raptorpanel.net` works once [the static sites](#static-sites) are up; `systemctl list-timers 'raptor-*'` shows both timers.

### Server #2

```bash
cat > /opt/raptor/deploy/replica/.env <<'EOF'
PRIVATE_IP=10.0.0.3
PRIMARY_PRIVATE_IP=10.0.0.2
EOF
echo ROLE=replica > /etc/raptor/stack.env

cd /opt/raptor/deploy/replica
../scripts/secrets.sh
docker compose up -d --wait postgres
```

On its first start it makes a replication slot on server #1, copies the database (`pg_basebackup`), and follows it from then on. Pull the Panel image now too, so a failover doesn't wait on a download: `docker compose --profile failover pull` (with `PANEL_IMAGE` set to the version on server #1, also written to this `.env` file; do it again after each deploy, or let the failover runbook do it).

**Check**, on server #1:

```bash
docker compose -f /opt/raptor/deploy/primary/compose.yaml exec -u postgres postgres \
  psql -c "SELECT client_addr, state, sync_state, replay_lag FROM pg_stat_replication"
```

One row, `streaming`. The slot means server #1 keeps WAL the replica hasn't received yet: if server #2 is gone for long, server #1's disk fills. Watch it ([Monitoring](#monitoring)), and if server #2 is gone for good, drop the slot: `SELECT pg_drop_replication_slot('replica1')`.

## Support bundles

`raptor doctor -upload` sends a node's diagnostics bundle to the Panel, which stores it in a bucket and gives the owner a code like `RPT-7K2M-QX9D` for support. Bundles are redacted, but they're still logs from people's machines, so:

- **A bucket of its own**, not the backup bucket (it can be at the same provider). Private.
- **The Panel's key can only upload**: `PutObject` on that bucket and nothing else, no listing, reading, or deleting. A compromised Panel then can't read anyone's bundles. On Backblaze B2, make an application key with **write-only** access to the bucket; on S3, a policy allowing only `s3:PutObject` on `arn:aws:s3:::<bucket>/bundles/*`. Its values go in the vault item `Support bundles storage`.
- **Support reads them with a different key** (read-only), kept by whoever does support, not on the servers. A bundle is at `bundles/<date>/<code>.tar.gz`, with the node ID (if it was signed), the uploader's address, and its SHA-256 in the object's metadata.
- **A lifecycle rule deleting bundles after 90 days** (B2: bucket settings → lifecycle → keep only for 90 days; S3: an expiration rule on `bundles/`). Support doesn't need them longer, and the less there is, the less can leak.

The Panel caps uploads at 32 MB, 10 a day per linked node, 3 a day per address for unlinked ones, and 1,000 a day overall.

## Static sites

Two Cloudflare Workers with static assets and no code: `raptor-app` serves the web app's build (`web/dist`) and sends every unknown path to `index.html`; `raptor-verify` serves the Turnstile page (`web/verify`). Their settings are in [`web/wrangler.jsonc`](../web/wrangler.jsonc) and [`web/wrangler.verify.jsonc`](../web/wrangler.verify.jsonc). They're in the same account as the zones: whoever controls the DNS account controls these hostnames anyway (DECISIONS #203).

The deploy token can only change Workers: My Profile → API Tokens → Create Token → Custom: **Account → Workers Scripts → Edit**, for this account only. No DNS or zone permissions. Put it in the vault (item `Cloudflare Workers deploy`).

The first deploy, from your Mac:

```bash
cd web && pnpm install --frozen-lockfile && pnpm build      # reads web/.env.production
export CLOUDFLARE_API_TOKEN=<workers token> CLOUDFLARE_ACCOUNT_ID=<account id>
pnpm dlx wrangler@4 deploy
pnpm dlx wrangler@4 deploy -c wrangler.verify.jsonc
```

Then attach the domains, once, in the dashboard: Workers & Pages → `raptor-app` → Settings → Domains & Routes → Add → **Custom domain** → `app.raptorpanel.net`, and the same for `raptor-verify` with `verify.raptorpanel.net`. Cloudflare creates the DNS records and certificates. The domains stay out of the config on purpose: deploying a custom domain from wrangler needs DNS edit permission (and, outside a terminal, wrangler overwrites conflicting DNS records), which the deploy token shouldn't have. Deploys leave domains they don't mention alone. Neither Worker has a `workers.dev` or preview address, so the custom domains are the only way in.

Both carry their own headers (`web/public/_headers`, `web/verify/_headers`): a Content-Security-Policy that lets the app run only its own scripts, call only the API, frame only the verify page, and never be framed; the verify page may only load Turnstile and only be framed by the app.

**Check:** the browser's console on `app.raptorpanel.net` shows no CSP errors through sign-in, the Turnstile check, and the account pages.

## Deploying

Tag a release from `main`, and it deploys itself:

```bash
git checkout main && git pull
git tag v0.x.y && git push origin v0.x.y
```

The release workflow builds and pushes the images. Then, for a stable tag (`vX.Y.Z`, not `-rc.1` and the like), it deploys the Panel to server #1 and then the web app and the Turnstile page, in that order. The Wings binaries in the same release still wait for you to sign them (`task release:sign`). Nodes only update from signed releases, and the signing key never goes near CI.

On the server, `deploy.sh` runs the migrations first. They must work with the version still running: add columns, backfill, and drop in a later release; never rename in one step. Then it replaces one Panel at a time. A Panel shutting down answers 503 to Caddy's health check, stops getting new requests, and hands its node connections to the other over 20 seconds; nodes reconnect with jitter. Browsers see nothing; game servers never notice. If the new version doesn't become healthy, the job fails with the old one still serving the other half. Fix it and tag a new version, or redeploy the previous one by hand (below). A failed deploy stops before the web app, so the web app never runs ahead of the API.

### Automatic deploys

Set up once, after server #1 is running:

1. **A deploy user on server #1** that can run one script as root and nothing else:

   ```bash
   useradd --system --create-home --shell /bin/bash deploy
   echo 'deploy ALL=(root) NOPASSWD: /opt/raptor/deploy/scripts/deploy-from-ci.sh *' > /etc/sudoers.d/raptor-deploy
   chmod 0440 /etc/sudoers.d/raptor-deploy && visudo -c
   ```

2. **Its key**, made on your Mac and kept only in GitHub and 1Password:

   ```bash
   ssh-keygen -t ed25519 -N '' -C raptor-deploy -f /tmp/raptor-deploy
   ```

   On server #1, put the public key in `/home/deploy/.ssh/authorized_keys` (owned by `deploy`, mode 0600) with its command forced:

   ```
   restrict,command="sudo /opt/raptor/deploy/scripts/deploy-from-ci.sh \"$SSH_ORIGINAL_COMMAND\"" ssh-ed25519 AAAA… raptor-deploy
   ```

   Whatever the workflow sends becomes one argument to `deploy-from-ci.sh`. The script accepts only a `vX.Y.Z` tag that exists on GitHub and is on `main`, checks out that tag's `deploy/` files, and runs `deploy.sh`. With this key someone can redeploy a release you already made, and nothing else.

3. **GitHub:** Settings → Environments → new environment **`production`**. Allow deployments from tags only (`v*`). Add these secrets:
   - `DEPLOY_SSH_KEY`: the private key file's contents. Then `rm -P /tmp/raptor-deploy*`.
   - `DEPLOY_HOST`: server #1's public address.
   - `DEPLOY_KNOWN_HOSTS`: the output of `ssh-keyscan -t ed25519 <server #1's address>`. Check its fingerprint against `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the server.
   - `CLOUDFLARE_WORKERS_TOKEN` and `CLOUDFLARE_ACCOUNT_ID`: the Workers-only token from [Static sites](#static-sites).

   If you want a final check before production, add yourself as a required reviewer on the environment: every deploy then waits for one click.
4. Settings → Variables → **`DEPLOY_ENABLED`** = `true`. Until it's set, releases build and stop there.

SSH stays open to the internet (GitHub's runners have no fixed addresses). It takes keys only, and this key can do nothing but the above.

### By hand

To roll back, or if GitHub is down. On server #1:

```bash
git -C /opt/raptor fetch --tags && git -C /opt/raptor checkout --detach v0.x.y
/opt/raptor/deploy/scripts/deploy.sh v0.x.y
```

## Backups

- **WAL:** every change is archived within 60 seconds (`archive_timeout`), so the most you can lose to a total loss of both servers is about a minute.
- **Backups:** daily at 04:15 UTC, full on Sundays, differential otherwise. Four weeks of fulls are kept, with all the WAL between them, so you can restore to any moment in the last four weeks.
- **Restore test:** monthly, on the 1st. It restores the latest backup into a scratch container (never touching the live database or the archive), starts it, and compares the migration version and the count of users, nodes, and orgs with the primary. A failed run exits non-zero: `systemctl status raptor-restore-test`.
- **Status:** `docker compose -f /opt/raptor/deploy/primary/compose.yaml exec -u postgres postgres pgbackrest --stanza=raptor info`.

Restoring for real, when both servers are lost: build a new server #1 as above up to the backup repository, but instead of `stanza-create`, stop Postgres and restore over its empty data directory:

```bash
cd /opt/raptor/deploy/primary
docker compose up -d --wait postgres && docker compose stop postgres
docker compose run --rm --no-deps -u postgres --entrypoint bash postgres -c \
  'rm -rf /var/lib/postgresql/18/docker/* && pgbackrest --stanza=raptor restore'
docker compose up -d --wait postgres
```

Add `--type=time "--target=2026-10-07 12:00:00+00"` to the restore to stop at a moment (before a bad migration, say). These exact commands aren't rehearsed, but the restore they run is the one the monthly test runs.

## Failover

When server #1 is gone (or will be for longer than you're willing to be down), on server #2:

```bash
cd /opt/raptor/deploy/replica
# 1. Make sure server #1's Postgres is really down: two primaries would split.
#    If you can reach it: docker compose -f ../primary/compose.yaml stop
#    If you can't: power it off from the provider's console.
# 2. Promote, then checkpoint (until one, pgBackRest refuses to back up).
docker compose exec -u postgres postgres psql -c "SELECT pg_promote()"
docker compose exec -u postgres postgres psql -c "CHECKPOINT"
# 3. Start the Panels and Caddy here, at the version server #1 ran.
echo PANEL_IMAGE=ghcr.io/xena-studios/raptor-panel:v0.x.y >> .env
docker compose --profile failover up -d --wait
printf 'ROLE=replica\nCOMPOSE_PROFILES=failover\n' > /etc/raptor/stack.env
```

4. In Cloudflare, point `api` (A and AAAA) at server #2's public address. Proxied records change within seconds, and nodes and browsers reconnect by themselves.
5. Backups now run here: the timers read `ROLE` from `/etc/raptor/stack.env`. `systemctl enable --now raptor-backup.timer raptor-restore-test.timer`, and run `../scripts/backup.sh full` once.
6. Update the firewall's Postgres rule if server #1 comes back at a new address.

The rehearsal does steps 2–5 and checks the API answers and the promoted database takes writes and backs up.

**Afterwards:** don't fail back in a hurry. Rebuild server #1 as the new replica: wipe its Postgres volume (`docker compose -f /opt/raptor/deploy/primary/compose.yaml down -v`), then follow [Server #2](#server-2) on it with the addresses swapped. The servers have now swapped roles, which is fine to leave as it is. If you'd rather swap back, do a planned failover the same way at a quiet hour, after stopping the Panels on the current primary and waiting for `replay_lag` to reach zero, so nothing is lost.

## Monitoring

The Panel sends metrics and a sample of traces (10% of requests) over OpenTelemetry when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Grafana Cloud's free tier holds more than launch needs.

1. Grafana Cloud → your stack → **OpenTelemetry** → configure: make a token, and copy the endpoint (`https://otlp-gateway-<region>.grafana.net/otlp`) and the `Authorization=Basic%20…` header it shows. Put them in the vault item **`Grafana Cloud OTLP`** as `endpoint` and `headers`. `panel.env.tpl` already reads them.
2. Deploy. Both Panels report as `raptor-panel`, each with its container's hostname as the instance.
3. **Synthetic Monitoring** (in the same stack, free): an HTTP check on `https://api.raptorpanel.net/healthz` every minute from three locations. This is the "API is down" alarm, and it works when the Panel can't report anything.
4. **healthchecks.io** (free): two checks, *Raptor backup* (daily, 2 h grace) and *Raptor restore test* (monthly, 1 day grace). Put their ping URLs in `/etc/raptor/stack.env` on whichever server runs the backups, as `BACKUP_PING_URL=…` and `RESTORE_TEST_PING_URL=…`. The scripts ping only when they succeed, so a failed run and a timer that never fired both alert.
5. **Alerts** (Grafana → Alerting), with a contact point that reaches your phone. Metric names as Grafana shows them, after OTLP's dots become underscores:

   | Alert | Rule | Why |
   |---|---|---|
   | API down | the synthetic check fails from 2 of 3 locations | Nobody can sign in or manage anything |
   | API errors | `rpc_server_duration_milliseconds_count` with `rpc_connect_rpc_error_code` in `internal`, `unknown`, or `unavailable`, over 5% of all requests for 5 min | Something is broken behind the API |
   | Nodes dropped | `sum(raptor_nodes_connected)` falls by 30% within 5 min | Cloudflare, the network, or the Panel is failing nodes |
   | Reconnect storm | `rate(raptor_nodes_connects_total[5m])` over 3× its usual level for 15 min, outside deploys | Connections keep failing and retrying |
   | Replica gone | `raptor_postgres_replicas` < 1 for 5 min | No failover target, and WAL piles up for the replica's slot |
   | Replica behind | `raptor_postgres_replica_lag_seconds` > 60 for 10 min | A failover would lose that much |
   | Archive failing | `raptor_postgres_archive_failures` increased in the last 15 min | Point-in-time recovery has a hole until it's fixed |
   | WAL piling up | `raptor_postgres_wal_size_bytes` > 10 GB | The disk fills next; usually a dead replica's slot or the archive |
   | Email failing | `raptor_emails_total{result="failed"}` > 0 for 10 min | Sign-in codes aren't arriving |
   | Mirror failing | `rate(raptor_mirror_sync_failures_total[10m])` > 0 for 30 min | Pages show stale servers |

   `raptor_commands_total` (by action and outcome) is for dashboards: a rise in `unreached` usually goes with a node drop.

Also watch, outside Grafana: the provider's disk and CPU alerts on both servers (set them in its console), and the `support@` inbox.

## Rehearsal

```bash
task deploy:rehearse
```

Runs this whole guide on your machine with stand-ins for the parts that need the internet (MinIO for the bucket, plain files for 1Password, a local CA for Cloudflare's certificates): builds the images, starts server #1's stack and checks Caddy refuses connections without the client certificate, starts the replica and checks it streams, takes a full backup and runs the restore test, deploys while probing the API every 100 ms (no request may fail), and finally fails over: stops server #1, promotes the replica, starts the Panels on server #2, and checks they answer, writes work, and backups continue. About three minutes. Run it after changing anything in `deploy/`; CI only checks the images build and the files parse.
