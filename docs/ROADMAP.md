# Roadmap

Raptor is built. Wings, the Panel, and the web app are done and run in production; what's left is proving them before launch, then bugs, small features, and releases. This file is that list: [the launch gate](#launch-gate), [what's left before launch](#before-launch), [the backlog](#backlog) of small features and known gaps, and [bigger work after launch](#after-launch). How it was built, phase by phase, is [at the end](#how-it-was-built).

Releases are in [CONTRIBUTING.md](../CONTRIBUTING.md#releases). Bugs and requests from people running Raptor come in on the Discord server; one that's taken on goes here, or straight into a pull request.

## Where things stand

*As of Oct 26, 2026.* Production runs v0.10.0's Panel and web app on raptor-1, with raptor-2 as a streaming replica over WireGuard, backups to B2, monitoring that pages a phone, and automatic deploys of every stable tag. v0.9.0 is the latest published release; v0.10.0 is deployed but still a draft until it's signed. A real node is linked. Billing (Polar, with its checkout and card form in the app since #230), Raptor Backup Storage, and the docs at [docs.raptorpanel.net](https://docs.raptorpanel.net) are live. Networks and managed databases (#248–#258) and Raptor Bot (#268, off until it's set up) shipped since Oct 19. Redeploying the Panel without touching games is proven: v0.4.2 deployed while a player stayed connected to a server on the production node.

None of the four launch gates has passed yet. What's left is mostly drills, operations, and the business, not code.

## Launch gate

All four must pass before launch.

| | Status |
|---|---|
| Install success rate ≥ 95% on supported OSes during the beta | Waits on the beta. `task e2e:bootstrap` passes on Debian 12, Debian 13, and Ubuntu 24.04 (amd64 nightly in CI, arm64 locally). |
| No known critical or high security issues | DOMPurify's advisories through Monaco are fixed in #270. An outside review of auth is still wanted. |
| Postgres failover tested | Built and rehearsed (`task deploy:rehearse`); not yet drilled on the real servers. |
| Billing tested end to end, failed payments and read-only mode included | Built and on; not yet run against real Polar. |

## Before launch

In order, in three stages. Each item is done when it's crossed off here.

### 1 · Unblock and harden

From the Xena Studios GitHub audit of Oct 22, 2026, and the nodes' overdue updates.

1. ~~**DOMPurify:**~~ `security` is a required check, and `pnpm audit` failed on DOMPurify ≤3.4.16 through `monaco-editor`, so no pull request could merge. Fixed by the override in `web/pnpm-workspace.yaml` (#270); Renovate's onboarding followed (#269).
2. ~~**Sign v0.10.0**~~ (published Oct 27), **and roll it out:** `panel rollout start v0.10.0` on raptor-1 ([DEPLOY.md](DEPLOY.md#updating-nodes)). No rollout is known to have run since v0.6.0, and this one brings nodes v0.9.0's Go fix (GO-2026-6809) too. Then check an org's Alerts page (migration 00048 turned email channels into the people they reached), and that a player can join and the console and files work, since v0.8.0 changed every node's firewall.
3. **One release after every node runs v0.6.0 Wings or newer:** turn `db/panel/pending/drop_node_display_address.sql` into a migration (#253), and remove the SFTP username fallback for old Wings (`nodelink.CapSFTPUsername` in the Panel, the `user.serverid` form in `internal/wings/sftp`). Check in Cloudflare that the old `n-…` and player subdomain records are gone (`dns_retired` empty); then the retiring code can go too.
4. ~~**Workflows:**~~ (#272) third-party actions (`goreleaser`, `docker/*`, `pnpm/action-setup`, `go-task/setup-task`) pinned to commits, with Renovate keeping the pins current, since the release jobs can push code and images; `pnpm/action-setup` and `go-task/setup-task` past GitHub's Node 20 deprecation; CodeQL for Go, TypeScript, and the workflows (not a required check).
5. ~~**`security.txt`**~~ (RFC 9116) on raptorpanel.net and docs.raptorpanel.net, and **issue forms** for bugs and feature requests (#273). The files expire on Oct 26, 2027 ([DEPLOY.md](DEPLOY.md#static-sites)).
6. **Cloudflare's injected scripts off** in the raptorpanel.net zone (Security → Bots: JavaScript Detections, Bot Fight Mode, and Web Analytics). The CSP blocks them anyway, but they break `web/scripts/verify-deployed.mjs`, the published check that the site matches the release; rerun it afterwards.
7. ~~**Housekeeping:**~~ the stale draft release (v0.4.2) deleted, the closed branches `docs/open-decisions` and `feat/hostname-timezone` deleted (both had landed through other PRs: the decisions table, and #206), and the database e2e test's MySQL failures (once on `main` on Oct 19, again on #272) were a real race, fixed in #277: an instance counted as running while its image's temporary setup server answered.

### 2 · Pass the launch gate

8. **Polar's settings for the embedded billing** ([DEPLOY.md](DEPLOY.md#billing)): the embedding host, customer portal seat and plan changes off, the branding, and the Backup Storage meter and its price ([DEPLOY.md](DEPLOY.md#charging-for-raptor-backup-storage), before plans start, since Polar keeps a subscription on the prices it began with). Then the **billing drill** in the sandbox and once in production on a throwaway org: the free month, a second node ending it, a card added and changed, an invoice, a failed payment, read-only mode, paying again, cancelling and keeping the plan.
9. **The failover drill** from raptor-1 to raptor-2 at a quiet hour ([DEPLOY.md](DEPLOY.md#failover)), about 5 minutes of Panel downtime; afterwards `DEPLOY_HOST` points at the new primary. First, the `raptor-replica` alerts in Grafana.
10. **An outside review of auth**, for the security gate.
11. **Watch the API latency:** the uptime check's `/healthz` sometimes averaged 3–4 s for 5 minutes from Oregon and Montreal on Oct 14 (normally 100–200 ms). If it pages, match the time against the Panel and Postgres metrics.

### 3 · Finish the product, then the beta

12. **Sign-in in production:** Google's and Discord's production OAuth apps (callbacks at `https://api.raptorpanel.net/oauth/<provider>/callback`; GitHub's is registered; Discord's is Raptor Bot's application, [DEPLOY.md](DEPLOY.md#discord)), then every sign-in method tried once. Hardware-key 2FA with no SMS recovery confirmed on Cloudflare, GitHub, 1Password, Grafana, B2, OVH, Resend, and Polar.
13. **Raptor Bot on the Discord server** ([DEPLOY.md](DEPLOY.md#discord)): take Administrator off its role, the 1Password items, the interactions endpoint, the ticket channel's permissions and buttons; then the Customer role's preview in the staff log, and removals on. Until then #open-a-ticket has no way to open one.
14. **Alerts' Telegram bot:** create it with @BotFather and put its token and a webhook secret in 1Password ([DEPLOY.md](DEPLOY.md#secrets)); until then the app doesn't offer Telegram.
15. ~~**`raptor uninstall [-wipe-data]`**~~ (#274, [WINGS.md](WINGS.md#uninstall)): stops the servers gracefully, removes Raptor's services, containers, networks, firewall, and mounts, and keeps server files, backups, and databases unless `-wipe-data`; the bootstrap e2e runs it on every supported OS.
16. **Docs:** the owner's guide at docs.raptorpanel.net covers how Raptor works, pricing, installing and moving from Pterodactyl, servers (console, settings, files, backups, schedules, add-ons), nodes (health, disks, system and firewall, updates), the team (members and access, alerts, activity, account security), eggs, the `raptor` command and `raptor doctor`, the security model, and help. The app links to it from the user menu, Connect node, the connection test, and failed installs. Left: screenshots in the guides. The CLI reference is checked against the code: the usage is made from one list of commands, and tests fail if a command isn't routed or the docs page misses one or names one that doesn't exist.
17. ~~**Raptor Backup Storage, charged:**~~ (#288, [DEPLOY.md](DEPLOY.md#charging-for-raptor-backup-storage)): use over the included 10 GB per node goes to a Polar meter once a day, charged with the plan at $0.012 per GB-month; off until the meter and price are made in Polar and `PANEL_POLAR_STORAGE_METER_ID` is set. Owners are emailed when a folder is turned off, 7 days before its data is deleted, and the day before.
18. **The exit test:** a non-technical tester, starting from a blank VPS, gets a Minecraft server that friends can join, without help, in under 15 minutes. What they trip on goes first in the backlog.
19. **The private beta:** 20–50 invited owners on real boxes (a mix of providers, amd64 and arm64), fixing every install failure and confusing screen as it comes, and watching [the metrics](#metrics).
20. **The load test:** `cmd/fakewings` held 500 nodes against the dev Panel; left is thousands, through Cloudflare, against a staging Panel.
21. **The status page** at `status.raptorpanel.net` off Cloudflare's DNS (a Better Stack page exists; its record is on Cloudflare).
22. **Last, the week-long soak** (`task soak:start`, then `soak:report` after a week), so it runs against the code that launches: scheduled restarts and backups and random Wings restarts with no unexpected game downtime.

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

Docker networks managed from the app, so separate communities on one node can't reach each other ([NETWORKS.md](NETWORKS.md)); then managed MariaDB, MySQL, PostgreSQL, Redis, Valkey, and MongoDB on them with browsers ([DATABASES.md](DATABASES.md)). All eight steps are built (#248–#258): networks, rules between them, instances and databases, the SQL browser, imports, exports, and read-only users, version changes and databases in backups, external access, and Redis and MongoDB with their key and document browsers.

### Decided

The owner took every recommendation (October 2026):

- **External access for Redis and MongoDB:** Redis and Valkey get a second, TLS-only port, published, while the plain one stays inside; MongoDB's stays off until someone asks (DECISIONS #331). Built.
- **Redis and MongoDB admin accounts over the network:** accepted as they are (#320, #332).
- **Instances backed up on their own:** no; every database is in its server's backups (#314, #332).
- **Which Redis to ship:** Valkey, the BSD fork, beside Redis (#333). Built.
- **MongoDB's default version:** 8.0, with 7.0 for older machines and kernels 6.19+ (#323, #332).

### Known gaps

- **Not tried with a real passkey yet:** going back after a version change, restoring one database from a backup, and turning external access on, off, or changing it. Their signing is tested, and the work behind them is tested on real engines, but nobody has clicked them through in the app with a node-trusted passkey.
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
| 4 · Web app | The app on `app.raptorpanel.net` under a strict CSP with a reproducible build: nodes, servers, console, files, schedules, backups, members, eggs and add-ons, the connection test, alerts, the audit log, the staff panel | The exit test is item 18 [before launch](#before-launch) |
| 5 · Beta and launch | Billing, Raptor Backup Storage, support access, the docs and Discord | In progress: [before launch](#before-launch) |
