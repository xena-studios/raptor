# Deploying the Panel

How to stand up `raptorpanel.net` from nothing: one server, Cloudflare, 1Password, backups, and the static sites. The files it uses are in [`deploy/`](../deploy). The steps that run on the server are rehearsed end to end on one machine (`task deploy:rehearse`, [below](#rehearsal)), except where a step says otherwise. The Cloudflare and provider steps can't be rehearsed; each ends with what to check.

The why behind these choices is in [PANEL.md](PANEL.md#hosting), [RELIABILITY.md](RELIABILITY.md#panel), and [DECISIONS.md](DECISIONS.md) (#200–#206, #344).

## What you're building

```
                       Cloudflare (proxy, WAF, Turnstile)
browsers ──https──▶  app.raptorpanel.net     (Worker: web/dist)
                     verify.raptorpanel.net  (Worker: web/verify)
                     raptorpanel.net         (Worker: site/dist; www redirects here)
                     docs.raptorpanel.net    (Worker: docs-site/dist)
                     get.raptorpanel.net     (redirect to the latest install.sh)
browsers, nodes ──▶  api.raptorpanel.net ──client cert──▶ the server
                                                         Caddy :443
                                                         ├─ panel-a ┐
                                                         └─ panel-b ┴─▶ Postgres
                                                                          │ WAL within a minute,
                                                                          │ nightly backups
                                                                          ▼
                                                         object storage at another provider (B2)
nodes' hostnames:    <short id>.raptornodes.net  (DNS only, written by the Panel)
```

- **One server** runs everything: Caddy (TLS from Cloudflare's Origin CA, and only Cloudflare's client certificate gets in), two Panel instances (so a deploy never takes the API down), and Postgres with pgBackRest. There is no standby: if the server dies, you make a new one ([Rebuilding](#rebuilding)).
- **Backups** go to object storage at a different provider, encrypted before they leave the server. Every WAL segment is archived within a minute, there's a backup every night, and a restore is tested every month, automatically.
- **Rebuilding is one command** on any fresh Debian or Ubuntu server, at any provider: `bootstrap.sh restore <version>` installs everything, restores the database from the backups, and starts the Panel. Then you point `api` at it.
- **Secrets** live in 1Password and are written to memory (`/run/raptor`) at boot. Nothing secret is on disk or in the repo, except the 1Password token that reads them.
- **The web app** is static, served by Cloudflare Workers (static assets, no code), deployed with a token that can do nothing else.

## Before you start

You need:

| What | Notes |
|---|---|
| A Linux server | Debian 12 or 13, or Ubuntu 24.04, x86-64 or arm64, at any provider: production is an Oracle Cloud Ampere A1 (2 OCPU, 12 GB, the Always Free allowance) in Ashburn. 2 vCPU and 4 GB is enough at launch (Postgres's settings assume 4 GB or more), with 40 GB of disk. Its public IPv4 is all `bootstrap.sh` needs; nothing else about the provider matters. On Oracle, see [Oracle Cloud](#oracle-cloud) first. |
| Object storage | S3-compatible, at a **different provider** from the server (a provider outage or account problem mustn't take the database and its backups together). Backblaze B2 or Wasabi are fine. One bucket (production: `raptor-panel-backups` on B2), private, and an access key that can only use that bucket. On B2, its lifecycle is **"Keep prior versions for this number of days: 7"**: pgBackRest's own expiry still frees the space within a week, but someone on the server can't destroy the backups at once. Set a storage cap with an alert at 80% (Caps & Alerts), so a runaway repository is noticed before uploads are refused. |
| Cloudflare | `raptorpanel.net` as a zone, on the free plan. |
| AWS Route 53 | `raptornodes.net`: registered there, and its hosted zone, which holds one record per node (10,000 by default). [Below](#raptornodesnet). |
| 1Password | A vault called **Raptor production**, and a service account that can read it (and nothing else). |
| Resend, Turnstile, OAuth apps | Already made: you have the keys. They go in the vault, below. |
| A machine with Go, Docker, and the `op` CLI | Your Mac. For making keys and running the rehearsal. |

## Secrets

Everything the servers need comes from the **Raptor production** vault. Make these items (field names matter: the templates in [`deploy/secrets/`](../deploy/secrets) read them):

| Item | Fields | How to make it |
|---|---|---|
| `Panel signing key` | `key` | `go run ./cmd/panel keygen /tmp/signing.key`, paste the file's one line, then `rm -P /tmp/signing.key`. Also save the printed public key in the item's notes. **Nodes pin this key when they link: losing it means relinking every node. Never rotate it casually.** |
| `Panel data key` | `key` | Same, a separate run. It encrypts TOTP secrets and the copies of nodes' backup keys: losing it turns off everyone's authenticator app and loses those copies (nodes still have their own). |
| `Postgres` | `superuser_password`, `panel_password` | Two random passwords: `openssl rand -hex 32` each. Hex, so they're safe in a database URL. (`replicator_password`, if the item has it, is from when there was a replica: unused.) |
| `Origin certificate` | `certificate`, `private_key`, `origin_pull_ca` | Caddy's certificate, its key, and the origin pull CA, each pasted whole (PEM). [Below](#raptorpanelnet), steps 3 and 4. |
| `Deploy SSH key` | `public_key` | The deploy key's public half (`ssh-ed25519 AAAA… raptor-deploy`), [Automatic deploys](#automatic-deploys). `bootstrap.sh` installs it for the deploy user. |
| `Backup storage` | `bucket`, `endpoint`, `region`, `access_key_id`, `secret_access_key`, `encryption_passphrase` | From the storage provider (endpoint without `https://`, e.g. `s3.us-west-004.backblazeb2.com`). The passphrase: `openssl rand -base64 48`. **Without it the backups can't be read: it's in 1Password and only there, so make sure the vault itself is backed up (1Password's emergency kit).** |
| `Resend` | `api_key` | The production key: sending access only, for `mail.raptorpanel.net`. |
| `Turnstile` | `secret_key` | The production widget's secret. |
| `GitHub OAuth` | `client_id`, `client_secret` | The production app (callback `https://api.raptorpanel.net/oauth/github/callback`). |
| `Google OAuth`, `Discord OAuth` | `client_id`, `client_secret` | When you've made them. A provider is offered once both its values are set. |
| `Route 53 DNS` | `access_key_id`, `secret_access_key`, `zone_id` | [Below](#raptornodesnet). |
| `Support bundles storage` | `bucket`, `endpoint`, `region`, `access_key_id`, `secret_access_key` | [Below](#support-bundles). |
| `Raptor Backup Storage` | `key_id`, `application_key`, `bucket_id`, `endpoint`, `region` | Optional: without it the Panel doesn't offer it. [Below](#raptor-backup-storage). |
| `Grafana Cloud OTLP` | `endpoint`, `headers` | [Below](#monitoring). |
| `Raptor Bot` | `token`, `public_key`, `guild_id`, `customer_role_id`, `support_role_id` | Optional: without it the bot is off. [Below](#discord). |
| `Telegram alerts bot` | `token`, `webhook_secret` | Optional: without it the app doesn't offer Telegram alerts. In Telegram, message @BotFather: `/newbot`, a name (`Raptor Alerts`) and a username ending in `bot`; the token is what it answers. `webhook_secret`: `openssl rand -hex 32`. Then in @BotFather, `/setuserpic` with the logo and `/setdescription` ("Alerts from Raptor. Connect a chat from your org's settings, Alerts."). The Panel points the bot's webhook at itself when it starts. |

The Google, Discord, Raptor Bot, `raptornodes.net` DNS, support bundle, Grafana, and Telegram items can wait: until an item exists, `secrets.sh` skips its lines ([`deploy/secrets/optional/`](../deploy/secrets/optional)) with a warning, and the Panel runs without that feature. The others are required.

Rotate anything that was ever pasted in a chat or a terminal history.

Then the service account: 1Password → Developer → Service accounts → new, **read-only** access to **Raptor production** only. Its token goes on the server as `/etc/raptor/op-token` (`bootstrap.sh` asks for it) and nowhere else: put a copy in the vault itself (item `Service account`) so you can rebuild the server.

## Cloudflare

### raptorpanel.net

1. **SSL/TLS → Overview:** Full (strict). **Edge Certificates:** Always Use HTTPS on, minimum TLS 1.2, TLS 1.3 on, HSTS on (max-age 6 months to start, include subdomains; add preload only once everything is on HTTPS for good).
2. **DNS:**

   | Name | Type | Content | Proxy |
   |---|---|---|---|
   | `api` | A | the server's public IPv4 | Proxied |
   | `app`, `verify`, `docs`, `@` | | made by Workers when you attach the domains, [below](#static-sites) | Proxied |
   | `www` | AAAA | `100::` | Proxied (a placeholder: the redirect rule answers) |
   | `get` | AAAA | `100::` | Proxied (a placeholder: the redirect rule answers) |
   | `grafana` | CNAME | `raptor.grafana.net` | **DNS only**, optional, and not set up in production (where Grafana is `raptor.grafana.net`): Grafana gets its own certificate ([Monitoring](#monitoring)) |

   No AAAA for `api`: Cloudflare answers users over IPv6 for a proxied name anyway, and an AAAA record would only add a second way from Cloudflare to the server to firewall.

   The Resend records for `mail` and the Email Routing records are already there.
3. **Origin certificate** (for Caddy): SSL/TLS → Origin Server → Create certificate: ECDSA, hostname `api.raptorpanel.net`, 15 years. Put the certificate and the key in the vault, item `Origin certificate`, fields `certificate` and `private_key`. They go to the server from there, into memory.
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

   `origin-pull-ca.pem` goes in the `Origin certificate` item too, as `origin_pull_ca`; put the CA key and client key in the vault (item `Origin pull CA`) and delete `/tmp/aop`. The certificates last 10 years; set a reminder for year 9.
5. **Security:**
   - **Bot Fight Mode off.** Wings is a bot, and on the free plan Bot Fight Mode can't be skipped for a path: it would challenge node connections, which can't solve challenges.
   - WAF → Custom rules → "API: no challenges": when `http.host eq "api.raptorpanel.net"`, **Skip** the remaining custom rules, rate limiting rules, Super Bot Fight Mode (if your plan lists it), and, under More components to skip, Browser Integrity Check and Security Level. Keep it first in the list. The Panel has its own rate limits and Turnstile; a Cloudflare challenge page in the middle of an API call or a node's WebSocket only breaks it, and Security Level challenges addresses with a poor reputation, which is what data center addresses, where nodes run, often have.
   - Security level: medium or lower. Under-attack mode breaks the API and nodes: don't turn it on for `api`.
6. **Network:** WebSockets on (the default). **Caching → Cache Rules:** a rule bypassing the cache for `api.raptorpanel.net`.
7. **Rules → Redirect Rules:** `get.raptorpanel.net` → `https://github.com/xena-studios/raptor/releases/latest/download/install.sh`, status 302. The script checks the binary against the SHA-256 it was generated with (`task release:sign`), so where it's served from isn't a trust boundary; GitHub's "latest" skips pre-releases.

   And `www.raptorpanel.net/*` → `https://raptorpanel.net/${1}`, status 301, preserving the query string.

**Check:** after the servers are up, `curl -fsS -o /dev/null -w '%{http_code}\n' https://api.raptorpanel.net/healthz` prints `200`, and `curl -k --resolve api.raptorpanel.net:443:<the server's IP> https://api.raptorpanel.net/healthz` from anywhere but Cloudflare fails (the firewall drops it; with the firewall off, Caddy wants a client certificate).

### raptornodes.net

Registered at Amazon Route 53, and served from its hosted zone (`Z0328517Y9QKX0XUWKOX`), in the personal AWS organization's member account "Raptor". It moved off Cloudflare in November 2026: a free Cloudflare zone made after September 2024 holds 200 records, one per node.

1. Node records are plain A (or AAAA) records, 60-second TTL, written by the Panel. They point straight at nodes: game traffic and SFTP can't go through a proxy.
2. The apex, `www`, and `*` are alias records to a CloudFront distribution whose function, [`deploy/aws/raptornodes-function.js`](../deploy/aws/raptornodes-function.js), answers the apex with what the domain is (and where to report abuse), redirects `www`, and answers every name that isn't a node with a 404. A node's own record always wins over the wildcard.
3. The Panel's key: an IAM user with only the `raptor-panel-dns` policy, which allows changing A and AAAA records in this zone (never the apex, `www`, or `*`), listing them, and only from the server's IPv4 address (`aws:SourceIp`). A rebuilt server has a new address: add it to the policy, or the Panel can't write node records. Put the key and the zone ID in the vault (`Route 53 DNS`).
4. DNSSEC: off for now (no DS record at the registry). When turning it on, enable signing in the hosted zone first, then add its DS record in Registered domains; never leave a DS record the zone doesn't sign for, or every validating resolver answers SERVFAIL for every node.

4. Later, before launch: submit `raptornodes.net` to the [Public Suffix List](https://publicsuffix.org) ([ARCHITECTURE.md](ARCHITECTURE.md#node-dns)).

### Accounts

Hardware-key 2FA (no SMS, no backup codes left lying around) on Cloudflare, the registrar, 1Password, GitHub, Resend, the server provider, and the storage provider. Registrar lock on both domains. Whoever controls DNS or the static host controls the code users run.

## The server

On a fresh server, as root:

```bash
git clone https://github.com/xena-studios/raptor.git /opt/raptor
/opt/raptor/deploy/scripts/bootstrap.sh new v0.x.y       # the first time: an empty database
/opt/raptor/deploy/scripts/bootstrap.sh restore v0.x.y   # every time after: from the backups
```

`v0.x.y` is the Panel version to run (the latest release). It asks for the 1Password service account token once, then does everything else ([`bootstrap.sh`](../deploy/scripts/bootstrap.sh)):

1. Removes the image's own iptables rules if it has them (Oracle's do), then installs Docker, the 1Password CLI, ufw, and automatic security updates, and turns off SSH passwords.
2. Checks out the deploy files at that version and fills `/run/raptor` from 1Password (`secrets.sh`, which checks every required item is there).
3. Makes the deploy user that releases deploy through, with the key from `Deploy SSH key` ([Automatic deploys](#automatic-deploys)).
4. Turns on the firewall and the boot units: SSH from anywhere (keys only), 443 from Cloudflare's addresses only, and nothing else. Postgres isn't published at all. Docker publishes ports around ufw, so 443 is filtered in Docker's own chain (`DOCKER-USER`), and `raptor-firewall.service` reapplies that at every boot, from the last list of Cloudflare's addresses it fetched if it can't fetch a new one (with no list at all, 443 stays closed). Restart it now and then to pick up changes to Cloudflare's list: `systemctl restart raptor-firewall`.
5. **new:** starts an empty Postgres and makes the backup repository. The first start creates the `panel` role ([`initdb/10-roles.sh`](../deploy/panel/initdb/10-roles.sh); the Panel isn't a superuser) and the repository ([`initdb/20-stanza.sh`](../deploy/panel/initdb/20-stanza.sh)), so the WAL written while the database is created can be archived. **restore:** asks you to confirm the old server's Postgres is stopped, then [`restore.sh`](../deploy/scripts/restore.sh) restores the latest backup and every WAL segment after it into an empty volume, starts it, and checks it archives.
6. Deploys the Panel at that version (`deploy.sh`: migrations, both Panels, the version recorded in `deploy/panel/.env` so a reboot starts the same one), and starts Caddy.
7. Turns on the backup, restore-test, and disk-check timers and takes a full backup.

It ends by printing the server's address and the last steps, which happen off the server: point the `api` record at it in Cloudflare, and set `DEPLOY_HOST` and `DEPLOY_KNOWN_HOSTS` in GitHub (it prints the `gh` commands). It can be run again after a failure; each step skips what's done, except that a restore refuses a server that already has a database.

**Naming:** each server gets a name it keeps for life, `raptor-` and four random hex digits (`echo raptor-$(openssl rand -hex 2)`), never reused and never a count, so a replacement leaves no gap and claims no order. Set it before `bootstrap.sh` (`hostnamectl set-hostname raptor-7f3a`): the first deploy takes it as `SERVER_NAME`, which labels the Panels' metrics (`raptor-7f3a-panel-a`), so a new server's graphs start fresh instead of joining the old one's. Use the same name for the provider's instance and its reserved IP. What you type stays the same across servers: one SSH alias, `raptor`, pointed at whichever is current.

Your provider's firewall, if it has one, should be open for 22 and 443 only. SSH takes keys only.

GHCR makes new packages private: after the first release, set `raptor-panel` and `raptor-postgres` to public (the organization's Packages → each package → Package settings).

**Check:** `https://api.raptorpanel.net/healthz` answers 200; signing in on `app.raptorpanel.net` works once [the static sites](#static-sites) are up; `systemctl list-timers 'raptor-*'` shows both timers; `../scripts/restore-test.sh` (in `deploy/panel`) passes in about a minute.

### Oracle Cloud

Production runs on Oracle Cloud's Always Free tier (DECISIONS #344): one `VM.Standard.A1.Flex` with 2 OCPU and 12 GB (the whole free Arm allowance since June 2026), Ubuntu 24.04 (Oracle has no Debian image), in the home region, Ashburn.

- **Upgrade the account to Pay As You Go** first. It still costs nothing within the free limits, but a free-only account's machines are reclaimed when they're idle for a week (CPU, network, and memory under 20%), which an idle Panel is, and free-only accounts are the ones terminated without warning. Set a budget alert at $1 that emails you; don't set anything that stops resources.
- **Networking:** one VCN with a public subnet and an internet gateway. In its security list (or a network security group on the instance): ingress TCP 22 from anywhere, TCP 443 from anywhere (the server's own firewall narrows it to Cloudflare, and keeps doing so when Cloudflare's list changes, which a security list wouldn't), nothing else. Reserve a public IP and attach it, so the address survives stopping the instance.
- **Out of host capacity:** Arm machines in Ashburn often aren't available. Try each availability domain, and try again later; Pay As You Go accounts get them far more easily.
- `bootstrap.sh` removes the image's own iptables rules (`netfilter-persistent`), which otherwise reject everything but SSH and fight with Docker.

## Support bundles

`raptor doctor -upload` sends a node's diagnostics bundle to the Panel, which stores it in a bucket and gives the owner a code like `RPT-7K2M-QX9D` for support. Bundles are redacted, but they're still logs from people's machines, so:

- **A bucket of its own**, not the backup bucket (it can be at the same provider). Private.
- **The Panel's key can only upload**: `PutObject` on that bucket and nothing else, no listing, reading, or deleting. A compromised Panel then can't read anyone's bundles. On Backblaze B2, make an application key with **write-only** access to the bucket; on S3, a policy allowing only `s3:PutObject` on `arn:aws:s3:::<bucket>/bundles/*`. Its values go in the vault item `Support bundles storage`.
- **Support reads them with a different key** (read-only), kept by whoever does support, not on the servers. A bundle is at `bundles/<date>/<code>.tar.gz`, with the node ID (if it was signed), the uploader's address, and its SHA-256 in the object's metadata.
- **A lifecycle rule deleting bundles after 90 days** (B2: bucket settings → lifecycle → keep only for 90 days; S3: an expiration rule on `bundles/`). Support doesn't need them longer, and the less there is, the less can leak.

The Panel caps uploads at 32 MB, 10 a day per linked node, 3 a day per address for unlinked ones, and 1,000 a day overall.


## Raptor Backup Storage

Customers' offsite backups ([PANEL.md](PANEL.md#raptor-backup-storage)), in one Backblaze B2 bucket. Use a B2 account of its own, not the one the Panel's own backups are in, so a mistake with one can't touch the other.

1. **The bucket:** name it exactly `raptor-server-backups` (Wings only accepts this name for Raptor storage, `internal/shared/hosted`; if it's taken, the constant changes and so does every Wings). **Private**, and its lifecycle set to **"Keep prior versions for this number of days: 30"** (`daysFromHidingToDeleting: 30`). Nodes' keys can't delete for good, so their deletes (Kopia's cleanup, or someone on a hacked node) only hide files; this keeps the hidden versions 30 days, then removes them. Not "keep only the last version": a hacked node could then destroy its offsite backups within a day. Not B2's default (keep every version) either: deleted data would be stored, and billed, forever. Note the bucket ID and its S3 endpoint (`s3.<region>.backblazeb2.com`).
2. **The Panel's key**, with the B2 command line (`brew install b2-tools`, then `b2 account authorize` with the account's master key; the web page's keys can't make keys): `b2 key create raptor-panel listKeys,writeKeys,deleteKeys,listBuckets,listFiles,readFiles,writeFiles,deleteFiles`. It's for all buckets, since B2 only lets those make keys, so use an account with nothing else in it. The Panel uses it to make each node a key limited to `orgs/<org>/nodes/<node>/` in the bucket, delete keys, list files to measure usage, and delete a turned-off node's files after 30 days. It's the most powerful key Raptor holds after the database password: anyone with it can read and delete every customer's (encrypted) backups.
3. **The vault:** `Raptor Backup Storage` with `key_id`, `application_key`, `bucket_id`, `endpoint` (without `https://`), and `region`. The next deploy sets `PANEL_BACKUP_STORAGE_*`, and the app offers it on every node's Backups tab.
4. **Check**, with a test node: turn it on, take a backup to it, and see the node's folder in the bucket. Then, with the node's key (from its `state.db`, `backup_destinations`, on the test node only) and the `b2` command line: listing or reading another node's folder is refused; deleting one of its own files only hides it (the old version is still there); deleting a version (`b2 file delete` with its ID) is refused; and a backup and Kopia's cleanup on the node still work. The next day, org settings → Backups shows the usage.

## Billing

Billing is off until the Panel has Polar's token and product ([PANEL.md](PANEL.md#billing-polar)); nothing is enforced without them. Try it all in Polar's **sandbox** first (`sandbox.polar.sh`, test cards, and `PANEL_POLAR_API=https://sandbox-api.polar.sh` on a development Panel), including a failed payment, before the production item goes in the vault.

1. **Polar → Settings → General → Features:** turn on seat-based pricing (in beta at the time of writing).
2. **Products → New:** "Raptor node", a monthly subscription, pricing **seat-based**, fixed $12 per seat. No trial on the product: the Panel asks for the free month at checkout, only for an org that hasn't had one. Note its ID.
3. **Settings → Developers → New token** (an organization access token) with the scopes `checkouts:write`, `subscriptions:read`, `subscriptions:write`, `customer_sessions:write`, and `events:write` (for [Backup Storage](#charging-for-raptor-backup-storage)), and no others. No expiry, like the deploy token; rotate it by hand if it leaks.
4. **Settings → Webhooks → Add endpoint:** `https://api.raptorpanel.net/billing/polar/webhook`, format **Raw**, events `subscription.*` (all of them) and `order.paid`, `order.refunded`. Copy its secret (`whsec_…`). The "API: no challenges" WAF rule already lets Polar's servers through; Polar doesn't follow redirects and gives up after 10 seconds.
5. **Settings → Customer portal:** turn **off** subscription seat management and plan changes: seats follow nodes, set by the Panel (the sweep would put them back, but an owner shouldn't see the choice). Owners do cards, invoices, and cancelling on Raptor's own Billing page, through the customer portal API; Polar's portal is only linked for the billing address, tax ID, and receipt email, and Polar's failed-payment emails link to it.
6. **Settings → Preferences → Embedding:** add `app.raptorpanel.net` (and `localhost:5173` in the sandbox). Polar refuses to be framed anywhere else, so without it the checkout falls back to Polar's own page after 20 seconds and the card form stays blank. Apple Pay and Google Pay don't work in embedded forms until Polar validates the domain (email support@polar.sh with the org's slug and the domain), and the app's `Permissions-Policy` also has to allow `payment` for `https://polar.sh` then; cards work without either.
7. **Settings → General, the branding Polar has:** name **Raptor**, the logo (the running raptor mark, square, on its dark background), website `https://raptorpanel.net`, support email, and the Discord invite under socials. They show on the checkout, receipts, invoices, and Polar's emails. That's all of it: Polar has no colors or fonts to set, and its checkout comes only in light and dark, which the app picks to match the user's theme.
8. 1Password item **Polar** in Raptor production: `token`, `product_id`, `webhook_secret`. The next deploy picks it up (`deploy/secrets/optional/polar.env.tpl`).

Then check: an org's Billing page offers the free month; checkout lands back on it and the plan appears within seconds (the webhook) or 5 minutes (the sweep). The Panel logs `billing: subscription updated` when it changes seats.

Orgs that already have nodes when billing turns on have 7 days to start a plan (they're emailed), then their nodes go read-only. Tell beta users before you turn it on.

### Charging for Raptor Backup Storage

Storage past the included 10 GB a node is charged through a meter in Polar ([PANEL.md](PANEL.md#billing-polar), DECISIONS #335). It's off until the Panel has the meter's ID, so deploying changes nothing until then. Set it up **before** billing goes live if you can: Polar keeps a subscription on the prices it started with, so plans started before the metered price exists aren't charged for storage (their Billing page says so). Do it in the sandbox first.

1. **Products → Meters → Create meter:** name "Backup Storage over the included", filter **Name equals `backup_storage`**, aggregation **Sum** of the property **`gb_months`**, unit Custom ("GB-month"). The filter and aggregation can't be changed once it has events. Note the meter's ID.
2. **Products → "Raptor node" → Edit → add a price:** **Metered**, the meter from step 1, pricing **per unit**, **$0.012** a unit (1.2 cents; Polar's API takes `unit_amount` in cents), no cap. Keep the seat price as it is. Save: new checkouts get both prices.
3. **The token:** add the **`events:write`** scope to the organization access token in the **Polar** item (Settings → Developers), or make a new one with the old scopes plus `events:write` and replace `token`. Without it the sweep logs `backup storage: … 403` and sends nothing.
4. **Plans started before this** (if any): they keep only the seat price. To charge them for storage, move each subscription onto the product's current prices in Polar's dashboard, if it offers that for the subscription (try it in the sandbox first), or leave them uncharged and tell their owners.
5. 1Password item **Polar storage meter** in Raptor production, field `meter_id` (the ID from step 1). It's a separate item so the **Polar** item's template keeps working without it; the next deploy sets `PANEL_POLAR_STORAGE_METER_ID` (`deploy/secrets/optional/polar-storage.env.tpl`).

Then check, on a test org with a plan and more than 10 GB a node in Backup Storage: after the next day's measurement the Panel logs `billing: backup storage sent to Polar`, the customer's page in Polar shows a `backup_storage` event with `gb_months`, the subscription's meter counts it, and the org's Billing page shows "This period so far". `backup_storage_billing` says what was sent or skipped for each org and day, and why. The staff Overview stops saying it isn't charged.

## Discord

Raptor Bot on the Raptor Panel server ([PANEL.md](PANEL.md#discord)). It runs inside the Panel: nothing to host. Its application, **Raptor Bot** in the Discord Developer Portal, is also Discord sign-in's, so the `Discord OAuth` item is this application's ID and secret.

1. **Developer Portal → Raptor Bot → General Information:** copy the **Public Key**. Set **Interactions Endpoint URL** to `https://api.raptorpanel.net/discord/interactions` *after* the Panel with the bot's settings is deployed: Discord checks it on save with a signed ping, and a bad one.
2. **Bot:** **Server Members Intent** on (members joining); Message Content off. Reset the token if it was ever anywhere but 1Password.
3. **OAuth2 → Redirects:** `https://api.raptorpanel.net/oauth/discord/callback` (sign-in, and linking). Copy the client secret.
4. **On the server, the bot's role** ("Raptor Bot") without Administrator. It needs Manage Roles, View Channels, Send Messages, Send Messages in Threads, Create Private Threads, Manage Threads, Embed Links, and Read Message History, and must sit **above Customer** to give it. In Wick, whitelist the bot: a pass over the roles can change many at once.
5. **The Customer role belongs to the bot from now on.** At first it only adds Customer; once you turn removals on, it also takes it from anyone who isn't a linked owner or admin of an org with a plan, given by hand or not. After the deploy, look at what it would remove (the staff log, or `… panel discord roles preview`), sort out anyone who should keep it (they link their account), then `… panel discord roles enforce on`.
6. **The ticket channel** (`#🎫│open-a-ticket`): @everyone can View Channel, Read Message History, and Send Messages in Threads, but not Send Messages, Create Public Threads, or Create Private Threads. Support and Leadership get Manage Threads there (they see every ticket).
7. 1Password item **Raptor Bot** in Raptor production: `token`, `public_key` (hex), `guild_id`, `customer_role_id`, `support_role_id` (the role a new ticket pings), and `log_channel_id` (the staff channel it reports tickets and role changes to, e.g. 🗂️│server-logs; the bot needs to send messages there). Item **Discord OAuth**: `client_id` (the application ID) and `client_secret`. The next deploy picks them up (`deploy/secrets/optional/discord-bot.env.tpl`, `discord-oauth.env.tpl`); then step 1's endpoint.
8. **The buttons**, once, from the server: `sudo docker compose -f /opt/raptor/deploy/panel/compose.yaml exec panel-a panel discord post tickets <channel-id> [message-id]` adds "Open a ticket" to the bot's message there (or posts one), and `… panel discord post link <channel-id>` posts "Link your Raptor account" (to #verify or #welcome). Copy IDs with Discord's Developer Mode on.
9. Cloudflare: nothing to do. The "API: no challenges" WAF rule covers the whole API host, so Discord's requests aren't challenged, like Polar's; Discord gives up after 3 seconds.

Then check: the staff log shows what the bot would take Customer from, before you turn removals on; the bot shows online with "Type /docs to search the guide"; the logs say `discord: this instance holds the gateway` on one instance; `/docs backups` answers; `/link` on an unlinked account sends you to Settings; a test ticket opens a private thread and closes.

## Static sites

Four Cloudflare Workers with static assets and no code: `raptor-app` serves the web app's build (`web/dist`) and sends every unknown path to `index.html`; `raptor-verify` serves the Turnstile page (`web/verify`); `raptor-site` serves the landing page (`site/dist`); `raptor-docs` serves the docs (`docs-site/dist`) and their 404 page. Their settings are in [`web/wrangler.jsonc`](../web/wrangler.jsonc), [`web/wrangler.verify.jsonc`](../web/wrangler.verify.jsonc), [`site/wrangler.jsonc`](../site/wrangler.jsonc), and [`docs-site/wrangler.jsonc`](../docs-site/wrangler.jsonc). They're in the same account as the zones: whoever controls the DNS account controls these hostnames anyway (DECISIONS #203).

The deploy token can only change Workers: My Profile → API Tokens → Create Token → Custom: **Account → Workers Scripts → Edit**, for this account only. No DNS or zone permissions. Put it in the vault (item `Cloudflare Workers deploy`).

The first deploy, from your Mac:

```bash
cd web && pnpm install --frozen-lockfile && pnpm build      # reads web/.env.production
export CLOUDFLARE_API_TOKEN=<workers token> CLOUDFLARE_ACCOUNT_ID=<account id>
pnpm dlx wrangler@4 deploy
pnpm dlx wrangler@4 deploy -c wrangler.verify.jsonc
cd ../site && pnpm install --frozen-lockfile && pnpm build && pnpm dlx wrangler@4 deploy
cd ../docs-site && pnpm install --frozen-lockfile && pnpm build && pnpm dlx wrangler@4 deploy
```

Then attach the domains, once, in the dashboard: Workers & Pages → `raptor-app` → Settings → Domains & Routes → Add → **Custom domain** → `app.raptorpanel.net`, and the same for `raptor-verify` with `verify.raptorpanel.net` `raptor-site` with `raptorpanel.net`, and `raptor-docs` with `docs.raptorpanel.net`. Cloudflare creates the DNS records and certificates. The domains stay out of the config on purpose: deploying a custom domain from wrangler needs DNS edit permission (and, outside a terminal, wrangler overwrites conflicting DNS records), which the deploy token shouldn't have. Deploys leave domains they don't mention alone. No Worker has a `workers.dev` or preview address, so the custom domains are the only way in.

Both carry their own headers (`web/public/_headers`, `web/verify/_headers`): a Content-Security-Policy that lets the app run only its own scripts, call only the API, frame only the verify page, and never be framed; the verify page may only load Turnstile and only be framed by the app. The app also requires **Trusted Types** (`require-trusted-types-for 'script'`), so no plain string reaches a sink that runs code or markup. The app's code inserts no markup; the editor's workers go through the `raptor-workers` policy (`web/src/lib/trusted-types.ts`: same origin, `/assets/` only); Monaco and xterm use their own policies. Policy names aren't restricted (no `trusted-types` directive), since Monaco creates some lazily in features a test wouldn't reach.

**Checked in CI:** `pnpm --dir web test:csp` (after a build) serves `dist/` with `_headers`' `/*` headers, the way the Worker does, and loads the pages that work without a session in headless Chromium. Any CSP violation or page error fails the build job. Pages behind sign-in aren't covered there, since there's no API.

**The landing page** (`site/public/_headers`) runs no scripts at all: `script-src 'self'` plus the hash of any inline script the build emits (`site/scripts/headers.mjs`; none today), nothing loaded from elsewhere, and no forms. CI's `site` job loads it under those headers in both themes and fails on any violation, page error, or failed request (`pnpm --dir site test:csp`).

**The docs** (`docs-site/public/_headers`) run only their own scripts: `script-src 'self'`, the hashes of Starlight's few inline scripts (theme and helpers, filled in by `docs-site/scripts/headers.mjs`), and `'wasm-unsafe-eval'` for search, which is Pagefind's WebAssembly reading an index on the same origin. Nothing loads from elsewhere and there are no forms. CI's `docs` job checks every internal link, then loads every page and the 404 under those headers in both themes, runs a search, and fails on any violation, page error, or failed request (`pnpm --dir docs-site test:csp`).

**`security.txt`** ([RFC 9116](https://www.rfc-editor.org/rfc/rfc9116)) is served at `/.well-known/security.txt` on the landing page and the docs (`site/public/.well-known/`, `docs-site/public/.well-known/`), pointing to SECURITY.md's address and private vulnerability reporting. Its `Expires` is **Oct 26, 2027**: move it on by a year in both files before then.

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

The release workflow builds and pushes the images. Then, for a stable tag (`vX.Y.Z`, not `-rc.1` and the like), it deploys the Panel to the server and then the web app, the Turnstile page, the landing page, and the docs. The Wings binaries in the same release still wait for you to sign them (`task release:sign`). Nodes only update from signed releases, and the signing key never goes near CI.

On the server, `deploy.sh` runs the migrations first. They must work with the version still running: add columns, backfill, and drop in a later release; never rename in one step. Then it replaces one Panel at a time. A Panel shutting down answers 503 to Caddy's health check, stops getting new requests, and hands its node connections to the other over 20 seconds; nodes reconnect with jitter. Browsers see nothing; game servers never notice. If the new version doesn't become healthy, the job fails with the old one still serving the other half. Fix it and tag a new version, or redeploy the previous one by hand (below). A failed deploy stops before the web app, so the web app never runs ahead of the API.

When a release changes the Caddyfile, `deploy.sh` restarts Caddy after the Panels: it reads its config only when it starts, and the container still sees the old file after a checkout. That drops every connection for about a second; browsers and nodes reconnect on their own.

Besides the load-balanced route, Caddy sends `/i/panel-a/…` and `/i/panel-b/…` to that one Panel, which knows its route from `PANEL_INSTANCE_ROUTE` (set in `compose.yaml`). A node opens a file transfer's connection by that route, to the instance that asked for it ([ARCHITECTURE.md](ARCHITECTURE.md#files-and-sftp)).

### Automatic deploys

Set up once:

1. **Its key**, made on your Mac and kept only in GitHub and 1Password:

   ```bash
   ssh-keygen -t ed25519 -N '' -C raptor-deploy -f /tmp/raptor-deploy
   ```

   Put the public key (`/tmp/raptor-deploy.pub`, one line) in the vault, item `Deploy SSH key`, field `public_key`. `bootstrap.sh` makes a `deploy` user on the server that can run one script as root and nothing else (`/etc/sudoers.d/raptor-deploy`), and puts that key in its `authorized_keys` with its command forced:

   ```
   restrict,command="sudo /opt/raptor/deploy/scripts/deploy-from-ci.sh \"$SSH_ORIGINAL_COMMAND\"" ssh-ed25519 AAAA… raptor-deploy
   ```

   On a server bootstrapped before the item existed, run `bootstrap.sh` again (it skips what's done) or add that line by hand.

   Whatever the workflow sends becomes one argument to `deploy-from-ci.sh`. The script accepts only a `vX.Y.Z` tag that exists on GitHub and is on `main`, checks out that tag's `deploy/` files, and runs `deploy.sh`. With this key someone can redeploy a release you already made, and nothing else. One deploy runs at a time (a lock on `/run/raptor-deploy.lock`; a second exits 75). Nothing the deploy starts keeps the lock: before v0.3.2, the 1Password CLI's background `op daemon` inherited it and refused every later deploy. If a deploy says another is running when none is, `sudo fuser -v /run/raptor-deploy.lock` shows what holds it.

2. **GitHub:** Settings → Environments → new environment **`production`**. Under Deployment branches and tags, choose Selected branches and tags and add a **tag** rule `v*`, so only release tags can use it. Add these secrets:
   - `DEPLOY_SSH_KEY`: the private key file's contents. Then `rm -P /tmp/raptor-deploy*`.
   - `DEPLOY_HOST`: the server's public address.
   - `DEPLOY_KNOWN_HOSTS`: `<address> ssh-ed25519 AAAA…`, the server's host key. `bootstrap.sh` prints both of these as `gh secret set` commands at the end.
   - `CLOUDFLARE_WORKERS_TOKEN`: the Workers-only token from [Static sites](#static-sites).

   And one environment variable (not secret): `CLOUDFLARE_ACCOUNT_ID`, from the right side of any zone's Overview page.

   If you want a final check before production, add yourself as a required reviewer on the environment: every deploy then waits for one click.
3. Settings → Variables → **`DEPLOY_ENABLED`** = `true`. Until it's set, releases build and stop there.

Each deploy also copies the boot unit and timers from the release (`deploy.sh`), so they always match the scripts they run.

SSH stays open to the internet (GitHub's runners have no fixed addresses). It takes keys only, and this key can do nothing but the above.

### By hand

To roll back, or if GitHub is down. On the server:

```bash
git -C /opt/raptor fetch --tags && git -C /opt/raptor checkout --detach v0.x.y
/opt/raptor/deploy/scripts/deploy.sh v0.x.y
```

### Updating nodes

Publishing a signed release (`task release:sign`) doesn't update anyone's node. A staged rollout does ([WINGS.md](WINGS.md#updates)): 5% of nodes for an hour, 25% for four hours, then everyone, halted once a tenth of a stage's updates fail. Nodes with automatic updates off, or pinned, are skipped. On the server:

```bash
cd /opt/raptor/deploy/panel
docker compose exec panel-a panel rollout start v0.x.y
docker compose exec panel-a panel rollout status     # also: pause, resume, cancel
```

### Staff

Staff are made on the server, once their account exists and has a passkey (docs/PANEL.md#staff):

```bash
cd /opt/raptor/deploy/panel
docker compose exec panel-a panel staff add you@example.com admin
docker compose exec panel-a panel staff list
```

## Backups

- **WAL:** every change is archived within 60 seconds (`archive_timeout`), so the most you can lose to losing the server is about a minute.
- **Backups:** daily at 04:15 UTC, full on Sundays, differential otherwise. Four weeks of fulls are kept, with all the WAL between them, so you can restore to any moment in the last four weeks.
- **Restore test:** monthly, on the 1st. It restores the latest backup into a scratch container (never touching the live database or the archive), starts it, and compares the migration version and the count of users, nodes, and orgs with the live database. A failed run exits non-zero: `systemctl status raptor-restore-test`.
- **Status:** `docker compose -f /opt/raptor/deploy/panel/compose.yaml exec -u postgres postgres pgbackrest --stanza=raptor info`.

## Rebuilding

When the server is gone, broken beyond a quick fix, or you're moving providers: make a new one and restore onto it. About 15 minutes, most of it the package installs, losing at most the last minute of writes.

1. **Make sure the old server's Postgres is stopped.** If you can reach it: `cd /opt/raptor/deploy/panel && docker compose stop`, and `systemctl disable raptor-stack raptor-backup.timer raptor-restore-test.timer raptor-disk-check.timer` so a reboot doesn't start it again. If you can't, power it off from the provider's console, or delete it. Two servers archiving into one repository ruin it.
2. On a fresh server, as root, at the version the old one ran (or the latest release):

   ```bash
   git clone https://github.com/xena-studios/raptor.git /opt/raptor
   /opt/raptor/deploy/scripts/bootstrap.sh restore v0.x.y
   ```

3. Do the last steps it prints: the `api` record in Cloudflare (proxied records change within seconds, and nodes and browsers reconnect by themselves), and `DEPLOY_HOST` and `DEPLOY_KNOWN_HOSTS` in GitHub. Add the new address to the Route 53 key's policy ([raptornodes.net](#raptornodesnet)).

To stop at a moment instead of the end of the archive (before a bad migration, say), add `--target-time "2026-11-05 12:00:00+00"`, and pick the version that ran at that moment: a newer one would migrate the restored database forward again. Versions before v0.14.0 can't be restored this way (their deploy files are laid out for two servers); use the oldest one that can, as long as its migrations are no newer than that moment's.

`restore.sh` refuses a volume that already holds a database. To restore over the live one on the same server, stop the stack and remove it first (`docker compose down -v` in `deploy/panel`: there's no undo, so make sure the backups are what you want with `pgbackrest info` first).

The rehearsal rebuilds a server from the backups every time it runs, and the restore it does is the one the monthly test does.

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
3. **The dashboard:** Dashboards → New → Import → upload [`deploy/grafana/raptor-panel.json`](../deploy/grafana/raptor-panel.json), and pick the stack's Prometheus data source (`grafanacloud-<stack>-prom`). It shows the API (requests, server errors, latency by method), nodes (connected, reconnects, commands by outcome, their own health: host disks low and doctor's checks failing or warning), email, the mirror, and Postgres (WAL archive, WAL on disk, connections). Counters that haven't counted anything yet, such as commands before the first node links, show "No data" until they do.
4. **Synthetic Monitoring** (in the same stack, free): an HTTP check on `https://api.raptorpanel.net/healthz` every minute from three locations, alerting when 2 of 3 fail. This is the "API is down" alarm, and it works when the Panel can't report anything. Its built-in latency alert (*HTTPRequestDurationTooHighAvg [5m]*) fires on any probe's 5-minute average over its threshold, with no waiting period, so with the default it pages a few times a day for nothing: Synthetic Monitoring → Alerts → set it to 2000 ms.
5. **healthchecks.io** (free): three checks, *Raptor backup* (daily, 2 h grace), *Raptor restore test* (monthly, 1 day grace), and *Raptor disk* (hourly, 2 h grace). Put their ping URLs in `/etc/raptor/stack.env` on the server, as `BACKUP_PING_URL=…`, `RESTORE_TEST_PING_URL=…`, and `DISK_PING_URL=…`. The scripts ping only when they succeed, so a failed run and a timer that never fired both alert. The disk check (`disk-check.sh`, hourly) reports a failure outright once the disk Postgres is on is 85% full (`DISK_LIMIT_PERCENT`): the Panel's metrics can't see the host's disks, and on one server a full disk stops the database. Deploys remove Panel images older than a week that nothing uses, so releases don't fill it.
6. **Alerts:** first a contact point that reaches your phone (Alerting → Contact points: the Grafana mobile app, or email, Discord, or a pager), set as the default notification policy's contact point. With **Grafana IRM** (the contact point type `oncall`, as in production: contact point *Raptor Phone* → IRM integration *Raptor Alerts*), the integration's default route needs an **escalation chain**, or IRM takes every alert and notifies nobody: IRM → Escalation chains → *Raptor*, one step "Notify users: you" marked **Important** (it uses the app's critical alerts, which get through silent mode), set on Integrations → *Raptor Alerts* → default route. Then send a test (the integration's "Send demo alert") and check the phone rings and the alert group's log says "sent mobile push important message". Until Oct 15, 2026 production's route had no chain, and four real alerts paged no one. Then the rules, which are in [`deploy/grafana/alerts.yaml`](../deploy/grafana/alerts.yaml) in Prometheus's rule format: Alerting → Alert rules → import them as Grafana-managed rules from that file (Grafana 12 and later), into a folder named `Raptor`. If your Grafana doesn't offer the import, make each rule by hand: its `expr` is the query, its `for` the pending period.

   | Alert | Fires when | Why it matters |
   |---|---|---|
   | API down (Synthetic Monitoring) | `/healthz` fails from 2 of 3 locations (it fails when the Panel can't reach Postgres, too) | Nobody can sign in or manage anything |
   | Panel silent | no Panel has sent metrics for 10 min | Every other alert is blind: the Panels are down, or the OTLP token expired or was revoked |
   | API errors | over 5% of requests answer internal, unknown, unavailable, or data loss, for 5 min | Something is broken behind the API |
   | Nodes dropped | connected nodes fall by 30% within 5 min (from at least 5) | Cloudflare, the network, or the Panel is failing nodes |
   | Reconnect storm | reconnects at 3× the day's rate for 15 min | Connections keep failing and retrying |
   | Archive failing | a WAL segment failed to archive in the last 15 min | Point-in-time recovery has a hole until it's fixed |
   | WAL piling up | over 10 GB of WAL for 10 min | The disk fills next; usually the archive failing |
   | Email failing | 3 or more emails failed in 15 min | Sign-in codes aren't arriving |
   | Mirror failing | mirror syncs failing for 30 min | Pages show stale servers |
   | Nodes failing a check | one of doctor's checks fails on 3 or more nodes and over a tenth of those reporting, for 2 h | Many at once points at Raptor (often a Wings release), not their owners; the Panels' logs name them ("node unhealthy") |
   | Nodes' disks low | 3 or more nodes and over a tenth of those reporting have a host disk below its minimum, for 2 h | They refuse installs and image pulls; many at once suggests Raptor is filling them |

   The API's request metric is `rpc_server_call_duration_seconds` (labels `rpc_method` and `rpc_response_status_code`, which is `OK` or the Connect code); `raptor_commands_total` (by `action` and `outcome`) is for the dashboard: a rise in `unreached` usually goes with a node drop.

Also watch, outside Grafana: the provider's disk and CPU alerts on the server (set them in its console), and the `support@` inbox.

## Rehearsal

```bash
task deploy:rehearse
```

Runs this whole guide on your machine with stand-ins for the parts that need the internet (MinIO for the bucket, plain files for 1Password, a local CA for Cloudflare's certificates): builds the images, starts the stack and checks Caddy refuses connections without the client certificate, takes a full backup and runs the restore test, deploys while probing the API every 100 ms (no request may fail), and finally destroys the server, volumes and all, and rebuilds it from the backups with `restore.sh`: it checks a write made after the last backup came back, the Panels answer, writes work, and backups continue. About three minutes. Run it after changing anything in `deploy/`; CI only checks the images build and the files parse. `bootstrap.sh`'s package and host setup isn't rehearsed (it needs a real machine); the parts after it are.
