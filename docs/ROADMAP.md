# Roadmap

Raptor is built. Wings, the Panel, and the web app are done and run in production; what's left is proving them before launch, then bugs, small features, and releases. This file is that list: [the launch gate](#launch-gate), [what's left before launch](#before-launch), [the backlog](#backlog) of small features and known gaps, and [bigger work after launch](#after-launch). How it was built, phase by phase, is [at the end](#how-it-was-built).

Releases are in [CONTRIBUTING.md](../CONTRIBUTING.md#releases). Bugs and requests from people running Raptor come in on the Discord server; one that's taken on goes here, or straight into a pull request.

## Where things stand

*As of Oct 19, 2026.* Production runs v0.7.0 on raptor-1, with raptor-2 as a streaming replica over WireGuard, backups to B2, monitoring that pages a phone, and automatic deploys of every stable tag. A real node is linked. Billing (Polar, with its checkout and card form in the app since #230), Raptor Backup Storage, and the docs at [docs.raptorpanel.net](https://docs.raptorpanel.net) are live. Redeploying the Panel without touching games is proven: v0.4.2 deployed while a player stayed connected to a server on the production node.

## Launch gate

All four must pass before launch.

| | Status |
|---|---|
| Install success rate ≥ 95% on supported OSes during the beta | Waits on the beta. `task e2e:bootstrap` passes on Debian 12, Debian 13, and Ubuntu 24.04 (amd64 nightly in CI, arm64 locally). |
| No known critical or high security issues | None known. An outside review of auth is still wanted. |
| Postgres failover tested | Built and rehearsed (`task deploy:rehearse`); not yet drilled on the real servers. |
| Billing tested end to end, failed payments and read-only mode included | Built and on; not yet run against real Polar. |

## Before launch

In order. Each is done when it's crossed off here.

1. **Finish v0.6.0 on the nodes:** v0.6.0 is signed and published, but nodes only update through a rollout: `panel rollout start v0.6.0` on server #1 ([DEPLOY.md](DEPLOY.md#updating-nodes)). Then check an org's Alerts page: migration 00048 turned email channels into the people they reached.
2. **One release after every node runs v0.6.0 Wings:** turn `db/panel/pending/drop_node_display_address.sql` into a migration (#253), and remove the SFTP username fallback for old Wings (`nodelink.CapSFTPUsername` in the Panel, the `user.serverid` form in `internal/wings/sftp`). Check in Cloudflare that the old `n-…` and player subdomain records are gone (`dns_retired` empty); then the retiring code can go too.
3. **Polar's settings for the embedded billing** ([DEPLOY.md](DEPLOY.md#billing)): the embedding host, customer portal seat and plan changes off, the branding. Then the **billing drill** in the sandbox and once in production on a throwaway org: the free month, a second node ending it, a card added and changed, an invoice, a failed payment, read-only mode, paying again, cancelling and keeping the plan.
4. **The failover drill** from raptor-1 to raptor-2 at a quiet hour ([DEPLOY.md](DEPLOY.md#failover)), about 5 minutes of Panel downtime; afterwards `DEPLOY_HOST` points at the new primary. First, the `raptor-replica` alerts in Grafana.
5. **Watch the API latency:** the uptime check's `/healthz` sometimes averaged 3–4 s for 5 minutes from Oregon and Montreal on Oct 14 (normally 100–200 ms). If it pages, match the time against the Panel and Postgres metrics.
6. **Sign-in in production:** Google's and Discord's production OAuth apps (callbacks at `https://api.raptorpanel.net/oauth/<provider>/callback`; GitHub's is registered; Discord's is Raptor Bot's application, [DEPLOY.md](DEPLOY.md#discord)), then every sign-in method tried once. Hardware-key 2FA with no SMS recovery confirmed on Cloudflare, GitHub, 1Password, Grafana, B2, OVH, Resend, and Polar.
7. **Raptor Bot on the Discord server** ([DEPLOY.md](DEPLOY.md#discord)): take Administrator off its role, check who has Customer by hand (the bot takes it from anyone who shouldn't have it), the 1Password items, the interactions endpoint, the ticket channel's permissions and buttons. Until then #open-a-ticket has no way to open one.
8. **Alerts' Telegram bot:** create it with @BotFather and put its token and a webhook secret in 1Password ([DEPLOY.md](DEPLOY.md#secrets)); until then the app doesn't offer Telegram.
9. **`raptor uninstall [-wipe-data]`:** documented from the start and never built. An owner leaving needs one command that unlinks, stops and removes Raptor's services, containers, networks, firewall table, and the data volume's mount, and keeps server files unless told otherwise; and a docs page.
10. **Docs:** the owner's guide at docs.raptorpanel.net covers how Raptor works, pricing, installing and moving from Pterodactyl, servers (console, settings, files, backups, schedules, add-ons), nodes (health, disks, system and firewall, updates), the team (members and access, alerts, activity, account security), eggs, the `raptor` command and `raptor doctor`, the security model, and help. Left: help links in the app (the user menu, Connect node, the connection test, failed installs), screenshots in the guides, and the CLI reference generated from the code so it can't drift.
11. **Raptor Backup Storage, charged:** use over the included 10 GB per node billed through a metered price in Polar, and the reminder emails before a turned-off folder's data is deleted that [SERVERS.md](SERVERS.md) promises.
12. **The exit test:** a non-technical tester, starting from a blank VPS, gets a Minecraft server that friends can join, without help, in under 15 minutes. What they trip on goes first in the backlog.
13. **The private beta:** 20–50 invited owners on real boxes (a mix of providers, amd64 and arm64), fixing every install failure and confusing screen as it comes, and watching [the metrics](#metrics).
14. **The load test:** `cmd/fakewings` held 500 nodes against the dev Panel; left is thousands, through Cloudflare, against a staging Panel.
15. **The status page** at `status.raptorpanel.net` off Cloudflare's DNS (a Better Stack page exists; its record is on Cloudflare).
16. **Last, the week-long soak** (`task soak:start`, then `soak:report` after a week), so it runs against the code that launches: scheduled restarts and backups and random Wings restarts with no unexpected game downtime.

### Business and legal

| When | Task |
|---|---|
| Now (overdue) | Business entity (Polar pays out to it), then the Polar account verified for payouts |
| Now | Register `raptornodes.com` if it's available and redirect it (`raptorpanel.com` belongs to someone else) |
| By launch | `raptornodes.net` on the Public Suffix List (takes weeks; less pressing since it only holds node hostnames, #279) |
| By the beta | Terms of Service, Privacy Policy, and a DPA covering support access and player data |
| Ongoing | Share progress publicly (devlogs, Discord) to build the beta list |

## Backlog

Small features and known gaps, unordered. Bugs found in the beta go first.

- **Nodes update when a release is published:** today an operator starts each rollout by hand ([DEPLOY.md](DEPLOY.md#updating-nodes)); the Panel could start one itself when a newer signed stable release appears.
- **`raptor jobs [logs <id>]`:** the node's jobs and their logs from the box, as [WINGS.md](WINGS.md#cli-scope) once planned.
- **Backups:** default destinations and retention for new servers (per node; turning on Raptor Backup Storage offers to add it to every server on the node); alerts when a destination starts or stops failing; adding a destination to several nodes at once.
- **Files:** a per-org limit on transfer speed through the Panel.
- **Connection test:** running it on its own after a server's first start.
- **`doctor`:** offering `xfs_repair` with the servers stopped when the data volume needs it.
- **Bulk actions** in the app (several servers at once), signed once over the whole list.
- **`TestNotifications`** on Wings' local API answers older CLIs only; it can go once no node runs a version before v0.5.0.

## Networks and databases

Docker networks managed from the app, so separate communities on one node can't reach each other ([NETWORKS.md](NETWORKS.md)); then managed MariaDB, MySQL, PostgreSQL, Redis, and MongoDB on them with browsers ([DATABASES.md](DATABASES.md)). All eight steps are built (#248–#258): networks, rules between them, instances and databases, the SQL browser, imports, exports, and read-only users, version changes and databases in backups, external access, and Redis and MongoDB with their key and document browsers.

### Waiting on a decision

Calls only the owner can make; each has a recommendation.

| Decision | Options | Recommendation |
|---|---|---|
| External access for Redis and MongoDB (DECISIONS #321) | Redis: a second, TLS-only port is published, the plain one stays inside. MongoDB: require TLS for everyone (plugins must be set up for it), or keep it off | Build Redis' TLS port; keep MongoDB off until someone asks |
| Redis and MongoDB admin accounts can log in over the network with their password (#320) | Accept (32 random characters, and the firewall keeps the owner's networks to servers with a database there), or run Wings' admin work through `docker exec` inside the container so the account can be kept to localhost | Accept for now; revisit if an instance on the default network worries anyone |
| Instances aren't backed up on their own (#314) | As now (every database is in its server's backups), or a whole-instance backup on its own schedule | As now |
| Which Redis to ship | Redis (7.4 and up are RSALv2/SSPL, 8 adds AGPLv3), or Valkey, the BSD fork, beside or instead | Add Valkey beside Redis; it's a drop-in for plugins |
| MongoDB's default version | 8.0 (needs a CPU from about 2016 on: ARMv8.2 or AVX), or 7.0 (runs anywhere, supported until 2027) | 8.0, with the app saying 7.0 is for older machines (it does) |

### Known gaps

- **Not tried with a real passkey yet:** going back after a version change, restoring one database from a backup, and turning external access on, off, or changing it. Their signing is tested, and the work behind them is tested on real engines, but nobody has clicked them through in the app with a node-trusted passkey.
- **Alerts:** an instance down or restarting, near its disk or connection limit, and a version change finished or failed aren't alert conditions yet; the outcome shows only on the instance.
- **The instance page's own browser** (admins, as the instance's administrator, across its databases) isn't built; each server's databases have theirs.
- **Long SQL queries:** results stop at 1,000 rows, 2 MB, and 50 seconds, with no cancel button, until results stream like file transfers (#310).
- **Readers of a SQL database** use the database's own user inside a read-only transaction (#309); switching them to the read-only users would be stronger.
- **Advanced engine settings:** the `name = value` lines checked against a safe list aren't built; only the common settings are fields.
- **`raptor doctor`** doesn't check networks or instances yet, and there's no `raptor db ps`.
- **MySQL 8 without TLS:** clients connecting in plain text must fetch the server's key (`allowPublicKeyRetrieval=true` for JDBC, `--get-server-public-key` for `mysql`); the docs should say so for plugin setups.
- **Patch updates** wait while an instance can still go back from a version change (#315), up to 7 days.
- **MongoDB 8.0+ on Linux 6.19+** is held back until MongoDB fixes SERVER-121912 (#323): watch for images that start, then drop the check in `versions.go` and bump the digests. Ubuntu 26.04 and newer cloud kernels are 6.19+.

## After launch

Bigger work, unordered.

- Server transfers between nodes (direct node-to-node)
- CurseForge for the mod manager (Modrinth, Hangar, datapacks, and Steam Workshop are built; CurseForge's terms and API key need a look first)
- A connection relay for owners behind CGNAT (a paid add-on)
- Queued config edits for offline nodes
- Public API keys and webhooks
- More certified games and OS versions
- End-to-end encryption of node traffic inside the WebSocket, so Cloudflare can't read it, if customers ask for it

## Metrics

To watch from the beta on.

| Metric | Why |
|---|---|
| Install success rate (by OS and provider) | Onboarding is the product's first impression |
| Time from signup to first running server | The core promise |
| Trial → paid conversion, **by box size** | Whether $12 a node works for small VPS owners |
| Support requests per node | Where the docs, `doctor`, or the app are failing |
| Game downtime caused by Raptor | Must stay at 0 |

## How it was built

Each phase ended with exit criteria, and the riskiest assumptions were checked by validation gates before the work that depended on them. The full checklists, with what each item became, are in this file's history (`git log -p -- docs/ROADMAP.md`, up to Oct 18, 2026); the decisions are in [DECISIONS.md](DECISIONS.md).

| Phase | What it built | How it was proven |
|---|---|---|
| 0 · Foundations | The monorepo, codegen (buf, sqlc), CI with lint, tests, licenses, and secret scanning, and signed releases | `v0.0.1-rc.1` built by CI, signed offline, and verified from its public URLs |
| 1 · Wings core | The egg runtime (Pterodactyl and Pelican eggs, unmodified images), the Docker runtime and firewall, the server lifecycle, the job engine and signed commands, disk quotas, the first CLI, and the egg conformance suite | Gates: Paper, Rust, and a Node.js egg running on arm64 and x86_64 (1.0); quotas that hold, survive reboots, and grow online (1.6) |
| 2 · Wings complete | Schedules, Kopia backups, SFTP, file operations, local metrics, `doctor`, the TUI, self-updates that roll back, Pterodactyl import, host disk protection | The fault-injection suite passes; the week-long soak is the last item [before launch](#before-launch) |
| 3 · Panel core | The production servers, accounts with passkeys, orgs and RLS, the node connection through Cloudflare, enrollment and `raptor bootstrap`, the mirror, node DNS, signed delegations, staged Wings updates | Gate: calls both ways through Cloudflare with idempotent retries (3.0); one command links fresh Debian 12, 13, and Ubuntu 24.04 VMs; a Panel redeploy in production that a connected player never noticed |
| 4 · Web app | The app on `app.raptorpanel.net` under a strict CSP with a reproducible build: nodes, servers, console, files, schedules, backups, members, eggs and add-ons, the connection test, alerts, the audit log, the staff panel | The exit test is item 11 [before launch](#before-launch) |
| 5 · Beta and launch | Billing, Raptor Backup Storage, support access, the docs and Discord | In progress: [before launch](#before-launch) |
