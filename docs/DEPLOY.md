# Deploying the Panel

How to stand up `raptorpanel.net` from nothing: two servers, Cloudflare, 1Password, backups, and the static sites. The files it uses are in [`deploy/`](../deploy). The steps that run on the servers are rehearsed end to end on one machine (`task deploy:rehearse`, [below](#rehearsal)), except where a step says otherwise. The Cloudflare and provider steps can't be rehearsed; each ends with what to check.

The why behind these choices is in [PANEL.md](PANEL.md#hosting), [RELIABILITY.md](RELIABILITY.md#panel), and [DECISIONS.md](DECISIONS.md) (#200–#206).

## What you're building

```
                       Cloudflare (proxy, WAF, Turnstile)
browsers ──https──▶  app.raptorpanel.net     (Worker: web/dist)
                     verify.raptorpanel.net  (Worker: web/verify)
                     raptorpanel.net         (Worker: site/dist; www redirects here)
                     get.raptorpanel.net     (redirect to the latest install.sh)
browsers, nodes ──▶  api.raptorpanel.net ──client cert──▶ server #1
                                                         Caddy :443
                                                         ├─ panel-a ┐
                                                         └─ panel-b ┴─▶ Postgres (primary)
                                                                          │  WAL, backups ──▶ object storage
                                                         WireGuard (wg0)  │                  (another provider)
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
| Two Linux servers | Same provider, **different datacenters** (raptor-1 is OVH Vint Hill, raptor-2 OVH Hillsboro), joined by a WireGuard tunnel ([the private link](#the-private-link)), so they don't need the provider's private networking. Debian 13 (or 12, or Ubuntu 24.04), x86-64 or arm64. 4 vCPU, 8 GB RAM, 80 GB NVMe each is plenty at launch; the Panel itself is small, Postgres gets the rest. |
| Object storage | S3-compatible, at a **different provider** from the servers (a provider outage or account problem mustn't take the database and its backups together). Backblaze B2 or Wasabi are fine. One bucket, private, versioning off (pgBackRest manages its own retention), and an access key that can only use that bucket. |
| Cloudflare | `raptorpanel.net` and `raptornodes.net` as zones. The free plan works for both to start; `raptornodes.net` needs a plan with enough DNS records once you pass the free plan's limit (one record per node). |
| 1Password | A vault called **Raptor production**, and a service account that can read it (and nothing else). |
| Resend, Turnstile, OAuth apps | Already made: you have the keys. They go in the vault, below. |
| A machine with Go, Docker, and the `op` CLI | Your Mac. For making keys and running the rehearsal. |

Pick the two servers' private addresses now; this guide calls them `10.0.0.2` (server #1) and `10.0.0.3` (server #2).

### Starting with one server

Server #1 runs everything by itself; server #2 only adds a standby. You can launch on one and add the second whenever an hour of downtime starts to matter:

- **What you give up:** a failover in minutes. Without server #2, recovering from a dead server #1 means a new server and a restore from the archive ([Backups](#backups)): 30 to 60 minutes, losing at most the last minute of writes (WAL is archived every 60 seconds). Zero-downtime deploys don't change: both Panels still run on server #1.
- **Size:** 2 vCPU, 4 GB RAM, 40 GB NVMe is enough at launch (Postgres's settings assume 4 GB or more). Pick a plan you can resize in place.
- **Skip:** private addresses (leave `PRIVATE_IP` out of `primary/.env`, and Postgres listens only on the server itself), [Server #2](#server-2), and the *Replica gone* and *Replica behind* alerts. There's no replication slot until a replica connects, so nothing holds WAL for one that doesn't exist.
- **Adding server #2 later:** set up [the private link](#the-private-link) on both servers (`apt-get install -y wireguard-tools` on server #1 first, and copy the new units: `cp /opt/raptor/deploy/systemd/* /etc/systemd/system/ && systemctl daemon-reload`), set `PRIVATE_IP=10.0.0.2` in server #1's `primary/.env`, then `systemctl enable --now raptor-wireguard && systemctl restart raptor-firewall` and `cd /opt/raptor/deploy/primary && docker compose up -d postgres` (a few seconds of database downtime). Then [Server #2](#server-2), and turn the two replica alerts on.

## Secrets

Everything the servers need comes from the **Raptor production** vault. Make these items (field names matter: the templates in [`deploy/secrets/`](../deploy/secrets) read them):

| Item | Fields | How to make it |
|---|---|---|
| `Panel signing key` | `key` | `go run ./cmd/panel keygen /tmp/signing.key`, paste the file's one line, then `rm -P /tmp/signing.key`. Also save the printed public key in the item's notes. **Nodes pin this key when they link: losing it means relinking every node. Never rotate it casually.** |
| `Panel data key` | `key` | Same, a separate run. It encrypts TOTP secrets and the copies of nodes' backup keys: losing it turns off everyone's authenticator app and loses those copies (nodes still have their own). |
| `Postgres` | `superuser_password`, `panel_password`, `replicator_password` | Three random passwords: `openssl rand -hex 32` each. Hex, so they're safe in a database URL. |
| `Backup storage` | `bucket`, `endpoint`, `region`, `access_key_id`, `secret_access_key`, `encryption_passphrase` | From the storage provider (endpoint without `https://`, e.g. `s3.us-west-004.backblazeb2.com`). The passphrase: `openssl rand -base64 48`. **Without it the backups can't be read: it's in 1Password and only there, so make sure the vault itself is backed up (1Password's emergency kit).** |
| `Resend` | `api_key` | The production key: sending access only, for `mail.raptorpanel.net`. |
| `Turnstile` | `secret_key` | The production widget's secret. |
| `GitHub OAuth` | `client_id`, `client_secret` | The production app (callback `https://api.raptorpanel.net/oauth/github/callback`). |
| `Google OAuth`, `Discord OAuth` | `client_id`, `client_secret` | When you've made them. A provider is offered once both its values are set. |
| `Cloudflare raptornodes.net DNS` | `token`, `zone_id` | [Below](#raptornodesnet). |
| `Support bundles storage` | `bucket`, `endpoint`, `region`, `access_key_id`, `secret_access_key` | [Below](#support-bundles). |
| `Raptor Backup Storage` | `key_id`, `application_key`, `bucket_id`, `endpoint`, `region` | Optional: without it the Panel doesn't offer it. [Below](#raptor-backup-storage). |
| `Grafana Cloud OTLP` | `endpoint`, `headers` | [Below](#monitoring). |

The Google, Discord, `raptornodes.net` DNS, support bundle, and Grafana items can wait: until an item exists, `secrets.sh` skips its lines ([`deploy/secrets/optional/`](../deploy/secrets/optional)) with a warning, and the Panel runs without that feature. The others are required.

Rotate anything that was ever pasted in a chat or a terminal history.

Then the service account: 1Password → Developer → Service accounts → new, **read-only** access to **Raptor production** only. Its token goes on each server as `/etc/raptor/op-token` (below) and nowhere else: put a copy in the vault itself (item `Service account`) so you can reinstall a server.

## Cloudflare

### raptorpanel.net

1. **SSL/TLS → Overview:** Full (strict). **Edge Certificates:** Always Use HTTPS on, minimum TLS 1.2, TLS 1.3 on, HSTS on (max-age 6 months to start, include subdomains; add preload only once everything is on HTTPS for good).
2. **DNS:**

   | Name | Type | Content | Proxy |
   |---|---|---|---|
   | `api` | A | server #1's public IPv4 | Proxied |
   | `app`, `verify`, `@` | | made by Workers when you attach the domains, [below](#static-sites) | Proxied |
   | `www` | AAAA | `100::` | Proxied (a placeholder: the redirect rule answers) |
   | `get` | AAAA | `100::` | Proxied (a placeholder: the redirect rule answers) |
   | `grafana` | CNAME | `raptor.grafana.net` | **DNS only** (Grafana gets its own certificate; [Monitoring](#monitoring)) |

   No AAAA for `api`: Cloudflare answers users over IPv6 for a proxied name anyway, and an AAAA record would only add a second way from Cloudflare to the server to firewall.

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
   - WAF → Custom rules → "API: no challenges": when `http.host eq "api.raptorpanel.net"`, **Skip** the remaining custom rules, rate limiting rules, Super Bot Fight Mode (if your plan lists it), and, under More components to skip, Browser Integrity Check and Security Level. Keep it first in the list. The Panel has its own rate limits and Turnstile; a Cloudflare challenge page in the middle of an API call or a node's WebSocket only breaks it, and Security Level challenges addresses with a poor reputation, which is what data center addresses, where nodes run, often have.
   - Security level: medium or lower. Under-attack mode breaks the API and nodes: don't turn it on for `api`.
6. **Network:** WebSockets on (the default). **Caching → Cache Rules:** a rule bypassing the cache for `api.raptorpanel.net`.
7. **Rules → Redirect Rules:** `get.raptorpanel.net` → `https://github.com/xena-studios/raptor/releases/latest/download/install.sh`, status 302. The script checks the binary against the SHA-256 it was generated with (`task release:sign`), so where it's served from isn't a trust boundary; GitHub's "latest" skips pre-releases.

   And `www.raptorpanel.net/*` → `https://raptorpanel.net/${1}`, status 301, preserving the query string.

**Check:** after the servers are up, `curl -fsS -o /dev/null -w '%{http_code}\n' https://api.raptorpanel.net/healthz` prints `200`, and `curl -k --resolve api.raptorpanel.net:443:<server #1 IP> https://api.raptorpanel.net/healthz` from anywhere but Cloudflare fails (the firewall drops it; with the firewall off, Caddy wants a client certificate).

### raptornodes.net

1. Every record is **DNS only** (grey cloud). Node hostnames point straight at nodes.
2. Apex: a Redirect Rule `raptornodes.net/*` → `https://raptorpanel.net`, 301 (with a proxied placeholder record `@ AAAA 100::` for it to answer on).
3. A token for the Panel: My Profile → API Tokens → Custom: permission **Zone → DNS → Edit**, zone **raptornodes.net only**, client IP filtering **every public address of server #1 and server #2, IPv4 and IPv6** (the Panel's calls leave over IPv4, since its Docker network has none, but commands on the servers themselves, like checking the token, prefer IPv6). The Panel holds this token and no other Cloudflare token; it can't touch `raptorpanel.net`. Put it and the zone ID in the vault (`Cloudflare raptornodes.net DNS`).
4. Later, before launch: submit `raptornodes.net` to the [Public Suffix List](https://publicsuffix.org) ([ARCHITECTURE.md](ARCHITECTURE.md#node-dns)).

### Accounts

Hardware-key 2FA (no SMS, no backup codes left lying around) on Cloudflare, the registrar, 1Password, GitHub, Resend, the server provider, and the storage provider. Registrar lock on both domains. Whoever controls DNS or the static host controls the code users run.

## Servers

### Both servers

As root, on a fresh install:

```bash
apt-get update && apt-get -y upgrade
apt-get install -y ca-certificates curl git gnupg ufw unattended-upgrades jq wireguard-tools
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

#### The private link

With two servers, Postgres replication runs over a private link between them. OVH's VPSs can't join a vRack, so it's a WireGuard tunnel (`wg0`, #268): server #1 is `10.0.0.2` and server #2 `10.0.0.3` on it, and nothing else uses it. On a lone server #1, skip this.

1. Make each server's key pair on your Mac (`brew install wireguard-tools`), once per server:

   ```bash
   wg genkey | tee /dev/stderr | wg pubkey
   ```

   The first line is the private key: put it in 1Password, Raptor production, as an item named **`WireGuard <the server's short hostname>`** (`WireGuard raptor-1`, `WireGuard raptor-2`) with the field `private_key`, and nowhere else. The second is the public key, which isn't secret.
2. On each server, its settings, naming the *other* server (on server #1 shown; on server #2, swap the addresses and use server #1's public key and public IP):

   ```bash
   cat >> /etc/raptor/stack.env <<EOF
   WG_ADDRESS=10.0.0.2/24
   PEER_PRIVATE_IP=10.0.0.3
   WG_PEER_PUBLIC_KEY=<the other server's public key>
   WG_PEER_ENDPOINT=<the other server's public IPv4>:51820
   EOF
   ```

3. Start it with the units below (`raptor-wireguard.service`), then check from either server: `wg show` lists the peer with a recent handshake, and `ping -c3 10.0.0.3` (or `.2`) answers. The private key goes from 1Password into the interface through a pipe: it's never written to disk.

The provider's firewall, if you use it, needs UDP 51820 open between the two servers too.

The firewall allows SSH, 443 from Cloudflare's addresses only, WireGuard's UDP port from the other server's public address only, and Postgres only from the other server through `wg0` (with no other server, from nowhere). Docker publishes ports around ufw, so 443 and Postgres are filtered in Docker's own chain (`DOCKER-USER`), and Postgres is also bound to the private address only. Those Docker rules don't survive a reboot by themselves, so `raptor-firewall.service` applies them at every boot, from the last list of Cloudflare's addresses it fetched if it can't fetch a new one (with no list at all, 443 stays closed). It runs below; restart it now and then to pick up changes to Cloudflare's list: `systemctl restart raptor-firewall`.

The boot unit, timers, and the server's settings:

```bash
cp /opt/raptor/deploy/systemd/* /etc/systemd/system/ && systemctl daemon-reload
systemctl enable --now raptor-wireguard.service   # with two servers only (above)
systemctl enable --now raptor-firewall.service
systemctl enable raptor-stack.service
ufw status && iptables -S DOCKER-USER | head -3
```

### Server #1

Its settings, read by Compose on every run (on one server, skip this: [Starting with one server](#starting-with-one-server)):

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

The first start creates the `panel` and `replicator` roles ([`initdb/10-roles.sh`](../deploy/primary/initdb/10-roles.sh); the Panel isn't a superuser) and the backup repository ([`initdb/20-stanza.sh`](../deploy/primary/initdb/20-stanza.sh)), so the WAL written while the database is created can be archived. `stanza-create` again is harmless. `check` archives a WAL segment and confirms it arrived: if it fails, the bucket settings or keys are wrong, and the container's log from the first start says why the repository wasn't made.

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
cat > /opt/raptor/deploy/replica/.env <<EOF
PRIVATE_IP=10.0.0.3
PRIMARY_PRIVATE_IP=10.0.0.2
SERVER_NAME=$(hostname -s)
EOF
echo ROLE=replica >> /etc/raptor/stack.env

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


## Raptor Backup Storage

Customers' offsite backups ([PANEL.md](PANEL.md#raptor-backup-storage)), in one Backblaze B2 bucket. Use a B2 account of its own, not the one the Panel's own backups are in, so a mistake with one can't touch the other.

1. **The bucket:** name it exactly `raptor-backup-storage` (Wings only accepts this name for Raptor storage, `internal/shared/hosted`; if it's taken, the constant changes and so does every Wings). **Private**, and its lifecycle set to **"Keep prior versions for this number of days: 30"** (`daysFromHidingToDeleting: 30`). Nodes' keys can't delete for good, so their deletes (Kopia's cleanup, or someone on a hacked node) only hide files; this keeps the hidden versions 30 days, then removes them. Not "keep only the last version": a hacked node could then destroy its offsite backups within a day. Not B2's default (keep every version) either: deleted data would be stored, and billed, forever. Note the bucket ID and its S3 endpoint (`s3.<region>.backblazeb2.com`).
2. **The Panel's key**, with the B2 command line (`brew install b2-tools`, then `b2 account authorize` with the account's master key; the web page's keys can't make keys): `b2 key create raptor-panel listKeys,writeKeys,deleteKeys,listBuckets,listFiles,readFiles,writeFiles,deleteFiles`. It's for all buckets, since B2 only lets those make keys, so use an account with nothing else in it. The Panel uses it to make each node a key limited to `orgs/<org>/nodes/<node>/` in the bucket, delete keys, list files to measure usage, and delete a turned-off node's files after 30 days. It's the most powerful key Raptor holds after the database password: anyone with it can read and delete every customer's (encrypted) backups.
3. **The vault:** `Raptor Backup Storage` with `key_id`, `application_key`, `bucket_id`, `endpoint` (without `https://`), and `region`. The next deploy sets `PANEL_BACKUP_STORAGE_*`, and the app offers it on every node's Backups tab.
4. **Check**, with a test node: turn it on, take a backup to it, and see the node's folder in the bucket. Then, with the node's key (from its `state.db`, `backup_destinations`, on the test node only) and the `b2` command line: listing or reading another node's folder is refused; deleting one of its own files only hides it (the old version is still there); deleting a version (`b2 file delete` with its ID) is refused; and a backup and Kopia's cleanup on the node still work. The next day, org settings → Backups shows the usage.

## Billing

Billing is off until the Panel has Polar's token and product ([PANEL.md](PANEL.md#billing-polar)); nothing is enforced without them. Try it all in Polar's **sandbox** first (`sandbox.polar.sh`, test cards, and `PANEL_POLAR_API=https://sandbox-api.polar.sh` on a development Panel), including a failed payment, before the production item goes in the vault.

1. **Polar → Settings → General → Features:** turn on seat-based pricing (in beta at the time of writing).
2. **Products → New:** "Raptor node", a monthly subscription, pricing **seat-based**, fixed $12 per seat. No trial on the product: the Panel asks for the free month at checkout, only for an org that hasn't had one. Note its ID.
3. **Settings → Developers → New token** (an organization access token) with the scopes `checkouts:write`, `subscriptions:read`, `subscriptions:write`, and `customer_sessions:write`, and no others. No expiry, like the deploy token; rotate it by hand if it leaks.
4. **Settings → Webhooks → Add endpoint:** `https://api.raptorpanel.net/billing/polar/webhook`, format **Raw**, events `subscription.*` (all of them) and `order.paid`, `order.refunded`. Copy its secret (`whsec_…`). The "API: no challenges" WAF rule already lets Polar's servers through; Polar doesn't follow redirects and gives up after 10 seconds.
5. **Settings → Customer portal:** let customers update their card, see invoices, and cancel. Leave plan changes off: seats follow nodes, set by the Panel.
6. 1Password item **Polar** in Raptor production: `token`, `product_id`, `webhook_secret`. The next deploy picks it up (`deploy/secrets/optional/polar.env.tpl`).

Then check: an org's Billing page offers the free month; checkout lands back on it and the plan appears within seconds (the webhook) or 5 minutes (the sweep). The Panel logs `billing: subscription updated` when it changes seats.

Orgs that already have nodes when billing turns on have 7 days to start a plan (they're emailed), then their nodes go read-only. Tell beta users before you turn it on.

## Static sites

Three Cloudflare Workers with static assets and no code: `raptor-app` serves the web app's build (`web/dist`) and sends every unknown path to `index.html`; `raptor-verify` serves the Turnstile page (`web/verify`); `raptor-site` serves the landing page (`site/dist`). Their settings are in [`web/wrangler.jsonc`](../web/wrangler.jsonc), [`web/wrangler.verify.jsonc`](../web/wrangler.verify.jsonc), and [`site/wrangler.jsonc`](../site/wrangler.jsonc). They're in the same account as the zones: whoever controls the DNS account controls these hostnames anyway (DECISIONS #203).

The deploy token can only change Workers: My Profile → API Tokens → Create Token → Custom: **Account → Workers Scripts → Edit**, for this account only. No DNS or zone permissions. Put it in the vault (item `Cloudflare Workers deploy`).

The first deploy, from your Mac:

```bash
cd web && pnpm install --frozen-lockfile && pnpm build      # reads web/.env.production
export CLOUDFLARE_API_TOKEN=<workers token> CLOUDFLARE_ACCOUNT_ID=<account id>
pnpm dlx wrangler@4 deploy
pnpm dlx wrangler@4 deploy -c wrangler.verify.jsonc
cd ../site && pnpm install --frozen-lockfile && pnpm build && pnpm dlx wrangler@4 deploy
```

Then attach the domains, once, in the dashboard: Workers & Pages → `raptor-app` → Settings → Domains & Routes → Add → **Custom domain** → `app.raptorpanel.net`, and the same for `raptor-verify` with `verify.raptorpanel.net` and `raptor-site` with `raptorpanel.net`. Cloudflare creates the DNS records and certificates. The domains stay out of the config on purpose: deploying a custom domain from wrangler needs DNS edit permission (and, outside a terminal, wrangler overwrites conflicting DNS records), which the deploy token shouldn't have. Deploys leave domains they don't mention alone. No Worker has a `workers.dev` or preview address, so the custom domains are the only way in.

Both carry their own headers (`web/public/_headers`, `web/verify/_headers`): a Content-Security-Policy that lets the app run only its own scripts, call only the API, frame only the verify page, and never be framed; the verify page may only load Turnstile and only be framed by the app. The app also requires **Trusted Types** (`require-trusted-types-for 'script'`), so no plain string reaches a sink that runs code or markup. The app's code inserts no markup; the editor's workers go through the `raptor-workers` policy (`web/src/lib/trusted-types.ts`: same origin, `/assets/` only); Monaco and xterm use their own policies. Policy names aren't restricted (no `trusted-types` directive), since Monaco creates some lazily in features a test wouldn't reach.

**Checked in CI:** `pnpm --dir web test:csp` (after a build) serves `dist/` with `_headers`' `/*` headers, the way the Worker does, and loads the pages that work without a session in headless Chromium. Any CSP violation or page error fails the build job. Pages behind sign-in aren't covered there, since there's no API.

**The landing page** (`site/public/_headers`) runs no scripts at all: `script-src 'self'` plus the hash of any inline script the build emits (`site/scripts/headers.mjs`; none today), nothing loaded from elsewhere, and no forms. CI's `site` job loads it under those headers in both themes and fails on any violation, page error, or failed request (`pnpm --dir site test:csp`).

**Check by hand:** the browser's console on `app.raptorpanel.net` shows no CSP errors through sign-in, the Turnstile check, and the account pages.

### Published bundle hashes

Every release says exactly what the web app is, so anyone can check that `app.raptorpanel.net` serves it unchanged:

- **The build is reproducible.** CI builds the app twice from scratch and fails if any file differs (`node web/scripts/bundle-hashes.mjs` both times).
- **Each release carries `web-bundle.sha256`.** It holds the SHA-256 of every file the app is built into, in `sha256sum`'s format. Goreleaser builds it from the tag (a before hook), and it's listed in `checksums.txt`, which the offline minisign key signs (`task release:sign`). So it's as trustworthy as the binaries.
- **Checking the live site:** verify `checksums.txt` with `release/minisign.pub`, check the manifest against it, then run `node web/scripts/verify-deployed.mjs web-bundle.sha256` (or pass another origin). It fetches every listed file from the site and fails on any difference. `_headers` is configuration, not served, so it's skipped. A local build of the tag can be checked with `sha256sum -c web-bundle.sha256` in `web/dist`.
- The deploy job builds the same commit with the same lockfile and Node version as the release, so a stable release's site matches its manifest. Between a tag and its deploy, the site still serves the previous release.

## Deploying

Tag a release from `main`, and it deploys itself:

```bash
git checkout main && git pull
git tag v0.x.y && git push origin v0.x.y
```

The release workflow builds and pushes the images. Then, for a stable tag (`vX.Y.Z`, not `-rc.1` and the like), it deploys the Panel to server #1 and then the web app, the Turnstile page, and the landing page. The Wings binaries in the same release still wait for you to sign them (`task release:sign`). Nodes only update from signed releases, and the signing key never goes near CI.

On the server, `deploy.sh` runs the migrations first. They must work with the version still running: add columns, backfill, and drop in a later release; never rename in one step. Then it replaces one Panel at a time. A Panel shutting down answers 503 to Caddy's health check, stops getting new requests, and hands its node connections to the other over 20 seconds; nodes reconnect with jitter. Browsers see nothing; game servers never notice. If the new version doesn't become healthy, the job fails with the old one still serving the other half. Fix it and tag a new version, or redeploy the previous one by hand (below). A failed deploy stops before the web app, so the web app never runs ahead of the API.

When a release changes the Caddyfile, `deploy.sh` restarts Caddy after the Panels: it reads its config only when it starts, and the container still sees the old file after a checkout. That drops every connection for about a second; browsers and nodes reconnect on their own.

Besides the load-balanced route, Caddy sends `/i/panel-a/…` and `/i/panel-b/…` to that one Panel, which knows its route from `PANEL_INSTANCE_ROUTE` (set in `compose.yaml`). A node opens a file transfer's connection by that route, to the instance that asked for it ([ARCHITECTURE.md](ARCHITECTURE.md#files-and-sftp)).

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

   Whatever the workflow sends becomes one argument to `deploy-from-ci.sh`. The script accepts only a `vX.Y.Z` tag that exists on GitHub and is on `main`, checks out that tag's `deploy/` files, and runs `deploy.sh`. With this key someone can redeploy a release you already made, and nothing else. One deploy runs at a time (a lock on `/run/raptor-deploy.lock`; a second exits 75). Nothing the deploy starts keeps the lock: before v0.3.2, the 1Password CLI's background `op daemon` inherited it and refused every later deploy. If a deploy says another is running when none is, `sudo fuser -v /run/raptor-deploy.lock` shows what holds it.

3. **GitHub:** Settings → Environments → new environment **`production`**. Under Deployment branches and tags, choose Selected branches and tags and add a **tag** rule `v*`, so only release tags can use it. Add these secrets:
   - `DEPLOY_SSH_KEY`: the private key file's contents. Then `rm -P /tmp/raptor-deploy*`.
   - `DEPLOY_HOST`: server #1's public address.
   - `DEPLOY_KNOWN_HOSTS`: the output of `ssh-keyscan -t ed25519 <server #1's address>`. Check its fingerprint against `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` on the server.
   - `CLOUDFLARE_WORKERS_TOKEN`: the Workers-only token from [Static sites](#static-sites).

   And one environment variable (not secret): `CLOUDFLARE_ACCOUNT_ID`, from the right side of any zone's Overview page.

   If you want a final check before production, add yourself as a required reviewer on the environment: every deploy then waits for one click.
4. Settings → Variables → **`DEPLOY_ENABLED`** = `true`. Until it's set, releases build and stop there.

SSH stays open to the internet (GitHub's runners have no fixed addresses). It takes keys only, and this key can do nothing but the above.

### By hand

To roll back, or if GitHub is down. On server #1:

```bash
git -C /opt/raptor fetch --tags && git -C /opt/raptor checkout --detach v0.x.y
/opt/raptor/deploy/scripts/deploy.sh v0.x.y
```

### Staff

Staff are made on server #1, once their account exists and has a passkey (docs/PANEL.md#staff):

```bash
cd /opt/raptor/deploy/primary
docker compose exec panel-a panel staff add you@example.com admin
docker compose exec panel-a panel staff list
```

## Backups

- **WAL:** every change is archived within 60 seconds (`archive_timeout`), so the most you can lose to a total loss of both servers is about a minute.
- **Backups:** daily at 04:15 UTC, full on Sundays, differential otherwise. Four weeks of fulls are kept, with all the WAL between them, so you can restore to any moment in the last four weeks.
- **Restore test:** monthly, on the 1st. It restores the latest backup into a scratch container (never touching the live database or the archive), starts it, and compares the migration version and the count of users, nodes, and orgs with the primary. A failed run exits non-zero: `systemctl status raptor-restore-test`.
- **Status:** `docker compose -f /opt/raptor/deploy/primary/compose.yaml exec -u postgres postgres pgbackrest --stanza=raptor info`.

Restoring for real, when both servers are lost: build a new server #1 as above up to the backup repository (its first start warns that the repository already holds another database: expected), but instead of `stanza-create`, stop Postgres and restore over its empty data directory:

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
echo COMPOSE_PROFILES=failover >> /etc/raptor/stack.env    # it already says ROLE=replica
```

4. In Cloudflare, point `api` (A and AAAA) at server #2's public address. Proxied records change within seconds, and nodes and browsers reconnect by themselves.
5. Backups now run here: the timers read `ROLE` from `/etc/raptor/stack.env`. `systemctl enable --now raptor-backup.timer raptor-restore-test.timer`, and run `../scripts/backup.sh full` once.
6. Update the firewall's Postgres rule if server #1 comes back at a new address.

The rehearsal does steps 2–5 and checks the API answers and the promoted database takes writes and backs up.

**Afterwards:** don't fail back in a hurry. Rebuild server #1 as the new replica: wipe its Postgres volume (`docker compose -f /opt/raptor/deploy/primary/compose.yaml down -v`), then follow [Server #2](#server-2) on it with the addresses swapped. The servers have now swapped roles, which is fine to leave as it is. If you'd rather swap back, do a planned failover the same way at a quiet hour, after stopping the Panels on the current primary and waiting for `replay_lag` to reach zero, so nothing is lost.

## Monitoring

The Panel sends metrics and a sample of traces (10% of requests) over OpenTelemetry when `OTEL_EXPORTER_OTLP_ENDPOINT` is set. Grafana Cloud's free tier holds more than launch needs.

1. A token that can only write metrics and traces. Don't use the token the **OpenTelemetry** tile makes: its policy (`stack-…-otlp-write`) also has `logs:write`, `profiles:write`, `alerts:write`, and `rules:write`, and anyone holding the last two can rewrite or delete your alert rules. Instead, Grafana Cloud → Administration → Users and access → **Cloud access policies** → Create:
   - Name `raptor-panel-otlp`, realm **your stack only**.
   - Scopes **`metrics:write`** and **`traces:write`**, nothing else. The Panel sends no logs or profiles.
   - Add token: name `raptor-panel`, expiry **No expiry**. An expired token fails quietly (the Panel only logs a warning and the dashboards go flat), and the token can't read anything, so rotate it by hand if it leaks rather than on a timer.

   Then Grafana Cloud → your stack → **OpenTelemetry** → configure, without generating a token there: copy the endpoint (`https://otlp-gateway-<region>.grafana.net/otlp`) and the instance ID. The header is `Authorization=Basic%20` followed by the base64 of `<instance ID>:<token>`. Paste the token at the silent prompt, so it stays out of your shell history:

   ```sh
   read -rs TOKEN; echo "Authorization=Basic%20$(printf '%s:%s' '<instance ID>' "$TOKEN" | base64 | tr -d '\n')"; unset TOKEN
   ```

   Put the endpoint and header in the vault item **`Grafana Cloud OTLP`** as `endpoint` and `headers`. `secrets.sh` picks them up on the next deploy.

   Optionally, its address: Grafana Cloud → your stack → Details → Edit (the instance settings) → Instance URL → custom domain `grafana.raptorpanel.net`, after the `grafana` CNAME in [DNS](#raptorpanelnet) exists. It must stay DNS only: Grafana proves the name to Let's Encrypt itself, which fails behind Cloudflare's proxy. If you ever add CAA records to `raptorpanel.net`, include `letsencrypt.org`. Only the sign-in address changes; the OTLP endpoint stays on `grafana.net`.
2. Deploy. Both Panels report as job `raptor-panel`, each with its container's hostname as the instance: `<server>-panel-a` and `<server>-panel-b` (`SERVER_NAME` in `.env`, which `deploy.sh` sets to the machine's short hostname the first time), so an instance keeps its series across deploys. In your stack → Explore, `raptor_postgres_pool_connections` should show two series within a minute.
3. **The dashboard:** Dashboards → New → Import → upload [`deploy/grafana/raptor-panel.json`](../deploy/grafana/raptor-panel.json), and pick the stack's Prometheus data source (`grafanacloud-<stack>-prom`). It shows the API (requests, server errors, latency by method), nodes (connected, reconnects, commands by outcome, their own health: host disks low and doctor's checks failing or warning), email, the mirror, and Postgres (replica, WAL archive, WAL on disk, connections). Counters that haven't counted anything yet, such as commands before the first node links, show "No data" until they do.
4. **Synthetic Monitoring** (in the same stack, free): an HTTP check on `https://api.raptorpanel.net/healthz` every minute from three locations, alerting when 2 of 3 fail. This is the "API is down" alarm, and it works when the Panel can't report anything.
5. **healthchecks.io** (free): two checks, *Raptor backup* (daily, 2 h grace) and *Raptor restore test* (monthly, 1 day grace). Put their ping URLs in `/etc/raptor/stack.env` on whichever server runs the backups, as `BACKUP_PING_URL=…` and `RESTORE_TEST_PING_URL=…`. The scripts ping only when they succeed, so a failed run and a timer that never fired both alert.
6. **Alerts:** first a contact point that reaches your phone (Alerting → Contact points: the Grafana mobile app, or email, Discord, or a pager), set as the default notification policy's contact point. Then the rules, which are in [`deploy/grafana/alerts.yaml`](../deploy/grafana/alerts.yaml) in Prometheus's rule format: Alerting → Alert rules → import them as Grafana-managed rules from that file (Grafana 12 and later), into a folder named `Raptor`. If your Grafana doesn't offer the import, make each rule by hand: its `expr` is the query, its `for` the pending period. Leave out the `raptor-replica` group until server #2 exists.

   | Alert | Fires when | Why it matters |
   |---|---|---|
   | API down (Synthetic Monitoring) | `/healthz` fails from 2 of 3 locations | Nobody can sign in or manage anything |
   | Panel silent | no Panel has sent metrics for 10 min | Every other alert is blind: the Panels are down, or the OTLP token expired or was revoked |
   | API errors | over 5% of requests answer internal, unknown, unavailable, or data loss, for 5 min | Something is broken behind the API |
   | Nodes dropped | connected nodes fall by 30% within 5 min (from at least 5) | Cloudflare, the network, or the Panel is failing nodes |
   | Reconnect storm | reconnects at 3× the day's rate for 15 min | Connections keep failing and retrying |
   | Archive failing | a WAL segment failed to archive in the last 15 min | Point-in-time recovery has a hole until it's fixed |
   | WAL piling up | over 10 GB of WAL for 10 min | The disk fills next; usually the archive or a dead replica's slot |
   | Email failing | 3 or more emails failed in 15 min | Sign-in codes aren't arriving |
   | Mirror failing | mirror syncs failing for 30 min | Pages show stale servers |
   | Nodes failing a check | one of doctor's checks fails on 3 or more nodes and over a tenth of those reporting, for 2 h | Many at once points at Raptor (often a Wings release), not their owners; the Panels' logs name them ("node unhealthy") |
   | Nodes' disks low | 3 or more nodes and over a tenth of those reporting have a host disk below its minimum, for 2 h | They refuse installs and image pulls; many at once suggests Raptor is filling them |
   | Replica gone (server #2) | no streaming replica for 5 min | No failover target, and WAL piles up for the replica's slot |
   | Replica behind (server #2) | replica over 60 s behind for 10 min | A failover would lose that much |

   The API's request metric is `rpc_server_call_duration_seconds` (labels `rpc_method` and `rpc_response_status_code`, which is `OK` or the Connect code); `raptor_commands_total` (by `action` and `outcome`) is for the dashboard: a rise in `unreached` usually goes with a node drop.

Also watch, outside Grafana: the provider's disk and CPU alerts on both servers (set them in its console), and the `support@` inbox.

## Rehearsal

```bash
task deploy:rehearse
```

Runs this whole guide on your machine with stand-ins for the parts that need the internet (MinIO for the bucket, plain files for 1Password, a local CA for Cloudflare's certificates): builds the images, starts server #1's stack and checks Caddy refuses connections without the client certificate, starts the replica and checks it streams, takes a full backup and runs the restore test, deploys while probing the API every 100 ms (no request may fail), and finally fails over: stops server #1, promotes the replica, starts the Panels on server #2, and checks they answer, writes work, and backups continue. About three minutes. Run it after changing anything in `deploy/`; CI only checks the images build and the files parse.
