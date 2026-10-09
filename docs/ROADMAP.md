# Roadmap

Every phase ends with a milestone that has **exit criteria**. Don't start the next phase until they pass. Later phases build directly on earlier ones, so cutting corners early costs more later.

```
Phase 0   Foundations
Phase 1   Wings core
Phase 2   Wings complete
Phase 3   Panel core
Phase 4   Web app
Phase 5   Beta + launch
          Business/legal track (runs alongside all phases)
```

**Critical path:** egg runtime → Wings core → node connection + enrollment → web app → billing. The egg runtime is the biggest risk: if it takes longer, everything after it moves.

There is no separate prototype phase. The riskiest assumptions are checked by a **validation gate** at the start of the phase that depends on them (1.0, 1.6, 3.0). If a gate fails, fix the design docs before continuing that phase.

## Next up

*As of Oct 8, 2026.* The Panel is live on one server (raptor-1, v0.1.0) with backups, monitoring, and automatic deploys. Phases 0–1 are met; Phase 2 is missing only its week-long soak; Phase 3 is built, and what's left is mostly proving it in production. In order:

1. **Link a real node to production** through `get.raptorpanel.net` and `api.raptorpanel.net`, and run a server on it. This closes the 3.0 gate's last box, and checks node DNS on `raptornodes.net`, the install script, and doctor's hostname check against the real zone. Then deploy a release while the server runs (Phase 3's exit criteria: no game impact).
2. **Test the alert chain end to end**: one alert from Grafana to the phone, plus the synthetic check failing for real.
3. **Start the week-long soak** (`task soak:start`); it runs on its own while the rest happens.
4. **Business track items that take weeks**: the Public Suffix List request for `raptornodes.net` (not on the list yet, and user subdomains wait for it), the business entity, `security@raptorpanel.net`.
5. **Phase 4, in the order the exit test needs it**: create-server wizard → console → files → node pages (live enrollment, health, remove) → backups → schedules → users → egg catalog → connection test → subdomains → audit log view.
6. **Before launch, not before**: server #2 and a failover drill (#212), billing, the load test, the docs site.

Found while checking production (Oct 8):
- `raptorpanel.net` itself answers nothing (no record), so `raptornodes.net`'s redirect lands on a dead page. The landing page is still to do.
- `raptorpanel.com` is registered at Namecheap and redirects to `gig.raptorsheets.com`. If that isn't yours, the "register `raptorpanel.com`" task isn't done; if it is, point it at `raptorpanel.net`.
- `status.raptorpanel.net` is a Better Stack page, but its DNS is on Cloudflare, which the plan said to avoid (a Cloudflare outage would take the status page down with the API).

---

## Phase 0 · Foundations

**Goal:** a repo where adding real code is fast and safe.

### Repo and tooling
- [x] Go module layout per [ARCHITECTURE.md](ARCHITECTURE.md#monorepo-layout)
- [x] `proto/` with `buf`: lint, breaking-change check, Connect codegen for Go + TypeScript
- [x] `sqlc` configured for Postgres (`db/panel`) and SQLite (`db/wings`)
- [x] Migration tooling for both (goose, forward-only, embedded in the binaries)
- [x] `web/`: Vite + React + TS + TanStack Router/Query + shadcn/ui + Tailwind, generated Connect client wired in
- [x] `Taskfile` for common commands
- [x] Dev environment: Docker Compose with Postgres (dev + test), plus a Lima Debian 12 VM for running Wings

### CI (GitHub Actions)
- [x] Go: build, `go vet`, `golangci-lint`, tests with race detector, static cross-compile
- [x] Web: typecheck, lint (Biome), build. Web tests start in Phase 4, when there's UI to test.
- [x] `buf lint` + `buf breaking` (on PRs)
- [x] DCO check (on PRs)
- [x] Generated code is up to date
- [x] License check (`go-licenses` + `scripts/check-web-licenses.mjs`): fail on non-AGPL-compatible licenses
- [x] `gitleaks`, `govulncheck`, `pnpm audit`

### Releases
- [x] Cross-compile `raptor` for linux/amd64 + linux/arm64 (static, no CGO) with GoReleaser
- [x] Release workflow producing a draft release with binaries + `checksums.txt`
- [x] minisign signing of `checksums.txt` with the offline key (`task release:sign`)
- [x] Generate the release key and commit `release/minisign.pub` (maintainer, offline)
- (The per-release install script with the embedded SHA-256 moves to Phase 3.3, alongside the install script itself.)

### Repository
- [x] Branch protection on `main`: PRs required, all CI jobs must pass, linear history, no force pushes or deletion

**Exit criteria:** `git push` runs all checks green; a tagged release produces signed binaries for both architectures.

✅ **Met.** CI is green on `main`; `v0.0.1-rc.1` was built by CI, signed offline, published, and verified from the public download URLs.

---

## Phase 1 · Wings core

**Goal:** a Wings daemon that runs egg-based servers reliably. No Panel yet; driven by a test harness and the CLI.

### 1.0 Validation gate: egg runtime
- [x] Read Pterodactyl Wings' source: environment building, container config, install process, stop handling, config parsers
- [x] Write down the exact env var list, container UID/GID, mounts, and entrypoint behavior in [EGGS.md](EGGS.md#runtime-environment)
- [x] Get **Paper**, **Rust**, and a **Node.js Discord bot** egg installing and running with unmodified yolks images
- **Gate:** all three run. This becomes the first real code of the egg engine, not throwaway.

✅ **Gate passed.** Paper (Pterodactyl and Pelican formats), Node.js (a stand-in app printing the egg's done string, since a real Discord bot needs a token), and Rust install, reach running, and stop cleanly on arm64 (dev VM) and x86_64 (GitHub runner). Rust is x86-only and was tested on x86_64 only.

### 1.1 Daemon skeleton
- [x] `raptor wings run`: config loading (strict `config.yml` per [WINGS.md](WINGS.md#config-file)), `slog` logging, graceful shutdown
- [x] SQLite: WAL, single writer + readers, migrations, `VACUUM INTO` snapshots (hourly + pre-migration), private (0600) files
- [x] Local socket API (`/run/raptor/wings.sock`, root + `raptor` group, `SO_PEERCRED` attribution) per [WINGS.md](WINGS.md#local-socket-api)
- [x] systemd unit (`OOMScoreAdjust=-900`, sandboxing options, `UMask=0077`), verified in the dev VM

### 1.2 Runtime ✅
- [x] `Runtime` interface + Docker implementation
- [x] `raptor_nw` network with collision-free subnet selection (plus `raptor_install`)
- [x] Labels `raptor.wings.*`; every query filtered by `raptor.wings.managed=true`; unlabeled containers and networks are never touched
- [x] Resource limits: memory + overhead, CPU weight (default) / hard limit / pinning, PID limit, `raptor.slice` with a memory ceiling
- [x] Hardening: non-root, cap drop, `no-new-privileges`, seccomp, only the server dir mounted
- [x] Port publishing for allocations; host-port-in-use check; optional host networking; `127.0.0.1` → gateway binding
- [x] Firewall: own nftables table `inet raptor` instead of a `DOCKER-USER` jump ([decision 56](DECISIONS.md)); install network isolation, metadata endpoint block, self-healing
- [x] `task e2e:runtime` + a CI job running it on every PR

### 1.3 Egg engine ✅
- [x] Parsers for `PTDL_v1`, `PTDL_v2`, `PLCN_v*`; reject unknown versions
- [x] Variables: Laravel-style rule validation (every rule used by 606 surveyed community eggs), validated **before** substitution
- [x] Install containers per [EGGS.md](EGGS.md#install): `/mnt/server` + read-only `/mnt/install`, hardening, limits, timeout, `raptor_install` network isolation, symlink-safe ownership fix, capped logs, failure behavior. "Wipe and reinstall" needed backups and came with them in Phase 2.
- [x] Runtime environment matching Pterodactyl Wings exactly (from 1.0)
- [x] Startup "done" detection, stop commands and signals, timeouts
- [x] Config file parsers: properties, yaml, json, ini, xml, file, all through `os.Root`, editing in place
- [x] Placeholders for both Pterodactyl and Pelican eggs
- [x] `x-raptor` extension parsing and validation
- [x] Arch check against image manifests
- [x] Fuzz tests: egg parsing, rules, placeholders, config parsers, path handling

### 1.4 Server lifecycle (per [SERVERS.md](SERVERS.md)) ✅
- [x] Server model with egg snapshots, `desired_state`, and config versions
- [x] State machine; idempotent power actions; create / install / reinstall / delete / start / stop / restart / kill with stop timeouts
- [x] Allocations: primary + extras, port range rules, host-port-in-use check, applied on next start
- [x] Console: always drained, 1,000-line ring buffer, output throttling, input limits, audit of commands
- [x] **Reconcile on Wings start**: reattach running containers, start servers with `desired_state=running` (staggered), refill console from Docker logs; resume after Docker restarts
- [x] `raptor-shutdown.service` for graceful stops on host shutdown, with container scopes ordered after it
- [x] Crash policy: crash detection (incl. OOM and clean exit), backoff, crash-loop stop, crash events with last console lines
- [x] Deletion: file cleanup and freed allocations. The final backup and orphaned offsite backups came with backups (Phase 2), quota cleanup with 1.6.
- [x] `task e2e:host`: Wings restart, Docker restart, host shutdown, and reboot, against the real systemd units

### 1.5 Job engine ✅
- [x] Durable queue in SQLite, resume/retry, per-server locks, global concurrency limits; installs run as resumable jobs
- [x] Job logs
- [x] Event outbox with monotonic `seq`, written in the same transaction as each change
- [x] `executed_commands` table for idempotency, also used as replay protection for signed commands
- [x] **Command envelope:** `command_id`, expiry, Panel grant, and optional passkey signature (`authenticatorData`, `clientDataJSON`, signature); RFC 8785 canonical form; which actions require a signature ([SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands))
- [x] **Signed-command verification in Wings:** trusted keys and delegations in SQLite, the full WebAuthn assertion check, rejection and logging of anything unsigned or invalid; tested with a software authenticator and fuzzed
- [x] Server actions (`server.create/update/delete/reinstall/start/stop/restart/kill/command`, `keys.add/remove`) wired to the manager and tested end to end on Docker

### 1.6 Disk quotas ✅
- [x] **Validation gate first:** on a fresh Debian 12 VM (ext4 root), loop image + systemd mount unit + project quotas set from Go. Fill past the limit, reboot, grow online, and benchmark against native disk. **Gate:** limits hold, survive reboot, grow online, within ~10% of native I/O. **Result:** limits hold (host, and root in containers), survive a real reboot, and grow online; I/O is at native speed except fsync'd writes, at about half ([WINGS.md](WINGS.md#disk-quotas)). The owner accepted that cost for hard limits by default (#92). The gate also found a quota escape from containers and a mount unit that systemd dropped at boot; both are fixed and tested (#93, #94).
- [x] Quota volume tiers 1–3; `raptor storage status|setup|grow`
- [x] Refuse to install or start servers if the volume isn't mounted with project quotas
- [x] Instant usage reporting; limit changes apply to running servers
- [x] `task e2e:quotas` (with a real reboot) and the same tests in CI, minus the reboot

### 1.7 CLI (first cut) ✅
- [x] `status`, `ps`, `start|stop|restart|kill`, `console`, `logs`, over the local socket API
- [x] `raptor` group can look, root can act; local actions attributed to the Unix user
- [x] Tested against the real daemon in `task e2e:host` (as root and as a `raptor` group member)

### 1.8 Egg conformance suite ✅
- [x] Harness: import → install → start → done → command → Wings restart → stop → reinstall ([EGGS.md](EGGS.md#conformance-test-suite))
- [x] Built-in catalog of certified eggs (`eggs/`, unmodified upstream copies + `raptor.yaml`)
- [x] Certified eggs in CI, in tiers: fast on amd64 + arm64, slow SteamCMD games weekly, CS2 by hand
- [x] Top ~50 community eggs (46 that pass, chosen as in [EGGS.md](EGGS.md#community-eggs); 18 that don't work were left out and listed)
- [x] Behavioral diff test against the real Pterodactyl Panel and Wings (every parser and placeholder form, and Paper); found and fixed a missing `/etc/machine-id` and the allocation limit's format
- [x] Symlink and path-traversal suite ([SECURITY-MODEL.md](SECURITY-MODEL.md#server-files)), checked against deliberately broken builds

**Exit criteria:**
- Paper, Rust, and a Discord bot install and run from the harness (Hytale moved to community: its server needs a Hytale account, #100)
- `systemctl restart raptor-wings` → **no game server restarts**, console history intact
- `systemctl restart docker` → servers keep running (`live-restore`)
- Conformance suite passes for all certified eggs (all automated tiers pass; CS2, the one manual egg, still needs its first run on an x86 box with a Steam login token)
- A symlink/path-traversal test suite passes

---

## Phase 2 · Wings complete

**Goal:** every node-side feature exists, tested against a stub Panel.

- [x] **Scheduler:** cron + timezone (with defined DST behavior), multi-step runs (`command`, `wait`, `power`, `backup`), `only_when_online`, jitter, missed-run policy, runs that resume after a Wings restart ([WINGS.md](WINGS.md#scheduler))
- [x] **Backups (Kopia):** local + S3 destinations, egg pre/post hooks, retention + maintenance jobs, safety backup before restore, a low-priority worker process, on by default through a daily schedule ([WINGS.md](WINGS.md#backups)). `raptor backup list|create|restore`. Deleting a server can take a **final backup** first (the server is kept if it fails; a local one is kept 30 days), and restore it onto another server (owner only); signed **wipe and reinstall** takes a safety backup first.
- [x] **SFTP:** off by default, enabled per node (`node.sftp`); `x/crypto/ssh` + `pkg/sftp`, `user.serverid` (full or short ID), `os.Root` chroot, host key generated on the node, auth callback interface (stubbed until the node connection), public key cache, per-address login limits ([WINGS.md](WINGS.md#files-and-sftp)). The egg's `file_denylist` applies to it as to the web file manager. Still to come: Panel auth (3.x), the `doctor` port check.
- [x] **File operations for the web file manager:** list, stat, read and write (for the editor, up to 4 MiB), mkdir, rename, copy, delete, chmod, compress and decompress (zip, tar, .tar.gz, .tar.bz2, .tar.zst, .gz, as jobs), all through `os.Root` in a `files` package that SFTP now shares; the egg's `file_denylist` (`.gitignore` syntax, through symlinks, and for directories holding denied files) for both; chunked, resumable uploads (staged in the server's directory, resumed after a Wings restart) and downloads, in chunks of up to 64 MiB, 1 GB per file. Read-only commands aren't stored with executed commands. The hostile-egg suite goes after the planted links through all of it ([WINGS.md](WINGS.md#files-and-sftp)). No HTTP server on the node. Still to come: the UI (Phase 4). (The separate connection that carries transfer chunks came with the node connection in 3.0.)
- [x] **Notifications:** Discord + generic webhooks (HMAC-signed) from Wings, including direct alerts for every signed dangerous action, run or rejected (configured in `config.yml` on the node, not through the Panel); crashes, failed backups and restores, low disk, failed installs, updates; cursors so restarts neither lose nor repeat them, retries, a per-target rate limit; `raptor notifications test` ([WINGS.md](WINGS.md#notifications))
- [x] **`raptor keys list|reset`** (pairing code shown on the box, the new key's fingerprint confirmed on the box before it's pinned) and **`raptor audit`** (a year of signed actions, run or rejected, and key resets, in the node's own `audit_log`) ([SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands))
- [x] **Local metrics:** every 10 s: CPU, memory, network, disk, and players (Minecraft status ping, Source A2S_INFO); minute rows for a day, 15-minute rows for a week, in SQLite; `GetMetrics` on the local API. Streaming to the Panel comes with the node connection ([WINGS.md](WINGS.md#local-metrics))
- [x] **`doctor`:** all checks from [WINGS.md](WINGS.md#doctor), with fix messages (runs without Wings; `-json`); `-bundle` (redacted). `-upload`, the Panel reachability check, and the node hostname check need the Panel and move to 3.3.
- [x] **TUI** (Bubble Tea): `raptor tui` with the server list (refreshed every 2 s), a server's stats, players, and an hour of CPU and memory history, its live console and a command line, and power and backup keys (root; the `raptor` group can look)
- [x] **Self-update:** `raptor update [-check] [-version v]`: channels (`stable`, `beta`) and a pin from GitHub releases, minisign verification with the trusted comment bound to the tag, SHA-256 check, and a **trial**: the running version starts the new one as its child and only switches the `current` link once it's healthy; a crash, 5 minutes without becoming healthy, or 3 interrupted starts roll back. Outcomes are recorded as `node.update` events. `task e2e:update` and a CI job test it against the real systemd unit ([WINGS.md](WINGS.md#updates)). Updates are started by hand until the Panel drives staged rollouts (3.3); "can't reconnect" joins the health check with the node connection.
- [x] **Pterodactyl coexistence** CI test + **`raptor import pterodactyl`**: servers listed through the Pterodactyl Panel's application API, eggs rebuilt as PTDL_v2, stopped through Pterodactyl's Wings, created installed in Raptor with their files copied (never moved or deleted), then suspended in Pterodactyl; `TestPterodactylImport` runs against the real Pterodactyl in CI ([WINGS.md](WINGS.md#pterodactyl-import))
- [x] **Host disk protection:** below `limits.host_disk_min_free` on any path Wings or Docker keeps state on (Docker's data directory, `state.db`, logs, tmp, local backups), new servers, reinstalls, and pulls of images that aren't on the node are refused; servers whose image is here still start. `node.disk_low`/`node.disk_ok` events and `raptor status` show it ([RELIABILITY.md](RELIABILITY.md#state-durability))
- [x] **Fault-injection suite** (`task e2e:faults`, in its own VM): Wings and Docker SIGKILLed mid-job, the host disk and a server's quota filled, the data volume unmounted, `state.db` corrupted (now restored from the newest good snapshot, with one taken on every clean stop); reboots are in `e2e:host`. Dropping the node connection comes with the connection (3.0). ([RELIABILITY.md](RELIABILITY.md#testing-for-reliability))

**Exit criteria:** fault-injection suite passes; a node survives a week-long soak test (scheduled restarts + backups + random Wings restarts) with no unexpected game downtime.

**Status:** every feature is built. The fault-injection suite passes (the node-connection fault comes with the connection in 3.0). The soak harness works (fast runs: no unexpected downtime through a dozen random Wings restarts), but the week-long run hasn't been done yet: `task soak:start`, then `soak:report` after a week.

---

## Phase 3 · Panel core

**Goal:** a real Panel that nodes connect to. Minimal UI.

### 3.0 Validation gate: node connection through Cloudflare
- [ ] Wings dials `wss://` through the real Cloudflare proxy; yamux inside; RPCs in both directions; challenge signatures both ways
- [x] Kill the connection mid-RPC → reconnect with jitter → retry with the same `command_id` → no duplicate execution
- [x] Hold connections idle for hours (ping keeps them alive), survive Cloudflare dropping them, and measure console latency while a 1 GB upload runs on its separate transfer connection
- **Gate:** RPCs work both ways through Cloudflare, retries are idempotent, idle connections stay up, console latency stays acceptable during a transfer. This becomes the real connection code.
- **Status:** the connection code is built and tested locally: the handshake (`internal/shared/nodelink`), Wings' side with reconnects and pings (`internal/wings/link`, started by Wings on a linked node), and the Panel's (`internal/panel/nodes`, a hub with retries); commands and events in both directions; cutting the connection mid-command (the command runs once and the retry gets its result) and a connection that silently stops passing bytes (closed on both sides); the transfer connection (opened by Wings when the Panel asks, for one upload or download only).
- **Through Cloudflare** (`task gate:link`, Oct 5, 2026: a quick tunnel, both sides on a home connection with 31 Mbps up): calls both ways at a 73 ms median round trip; the connection cut mid-command reconnected within a second, and the retry got the result of a command that ran once; a 1 GB upload on its transfer connection ran at 4.1 MB/s (the uplink), while calls on the main connection went from 123 ms to 145 ms at the 95th percentile (worst 770 ms). Held idle for 6 hours: calls every 15 minutes answered in 74–79 ms throughout; 4 drops (0.7 an hour), all while the quick tunnel itself was failing (Cloudflare answering 530, "tunnel not connected"), each reconnected by Wings on its own within seconds to a few minutes. Still to do: the same through `api.raptorpanel.net` on the real zone. A quick tunnel is Cloudflare's tunnel path, not the proxy in front of an origin that production uses; the edge's WebSocket handling is the same, but the gate's first box stays open until it's run on the real hostname.

### 3.1 Infrastructure
- [x] Server #1 (primary); Docker Compose; Caddy (live since Oct 8, 2026 on **raptor-1**, an OVH VPS-1 in Vint Hill running Debian 13, per [DEPLOY.md](DEPLOY.md); two Panels behind Caddy with per-hostname origin pulls, the firewall reapplied at boot; `task deploy:rehearse` still stands up both servers' stacks on one machine)
- [ ] Server #2 (Postgres replica): deliberately later (#212). It's needed before launch, whose gate includes a tested failover; the runbook and the rehearsal are ready, and DEPLOY.md's single-server section says what changes.
- [x] pgBackRest WAL archive to object storage in another location; **restore test** (Backblaze B2, `us-east-005`; encrypted WAL archive and backups, daily and monthly timers, and a daily restore test that compares the restored copy with the primary at one exact WAL position, each pinging a dead man's switch; passing in production)
- [x] DNS: hostnames from [ARCHITECTURE.md](ARCHITECTURE.md#hostnames); `api.raptorpanel.net` behind the Cloudflare proxy (origin locked to Cloudflare, WAF rule for node connections, no AAAA); `raptornodes.net` DNS-only, apex redirecting to `raptorpanel.net`; `app.` and `verify.` on Workers with strict CSP; `get.` redirecting to the latest `install.sh`
- [ ] Domain account hardening: hardware-key 2FA on the registrar and Cloudflare (no SMS recovery), registrar lock, scoped API tokens (both zones are on Cloudflare Registrar with transfers locked; every token is scoped (deploy: Workers only, DNS: `raptornodes.net` only, filtered by address); left: confirm hardware-key 2FA with no SMS recovery on Cloudflare, Namecheap (`raptorpanel.com`), GitHub, 1Password, Grafana, B2, OVH, and Resend)
- [x] Move code to the split hostnames: API on `api.raptorpanel.net` with CORS for `https://app.raptorpanel.net` only, node connections at `wss://api.raptorpanel.net/nodes/connect`, WebAuthn origin and RP ID `app.raptorpanel.net` in the Panel (Wings' defaults already point at `api.` and `app.` since 1.5)
- [x] Observability: OpenTelemetry → Grafana Cloud, alerts (stack `raptor` at `grafana.raptorpanel.net`; the Panel sends metrics and sampled traces over OTLP with a write-only token: every RPC's latency and result, connected nodes and connects, commands by outcome, emails, mirror sync failures, and Postgres replication, archive, and WAL health; the dashboard and alert rules are in [`deploy/grafana/`](../deploy/grafana), checked in CI, and imported; a synthetic check on `/healthz` from three regions; alerts page a phone through Grafana IRM; the backup and restore-test scripts ping a dead man's switch. Left: one alert sent end to end to the phone)
- [ ] Per-node event lag and Wings' own health metrics in the Panel's telemetry
- [x] Deploy pipeline: zero-downtime deploys, with node connections drained and reconnected with jitter (a stable release tag deploys itself: the release workflow runs `deploy/scripts/deploy.sh <tag>` on server #1 through an SSH key that can do nothing else, then publishes the web app; migrations, then one Panel at a time; a Panel shutting down fails its health check before draining; the rehearsal probes the API every 100 ms through a deploy without a failure)

### 3.2 Accounts
- [ ] Passwordless auth in the Panel ([PANEL.md](PANEL.md#auth)): passkeys, OAuth (Google, Discord, GitHub), email codes + links, TOTP + recovery codes, safe OAuth account linking (done in code and tested, with sign-in screens in the web app; GitHub's production app is registered; left: Google's and Discord's production apps, with callbacks at `https://api.raptorpanel.net/oauth/<provider>/callback` (their 1Password items are optional until then), and trying every method once in production)
- [x] Sessions: hashed tokens in a `__Host-` cookie on `api.`, device list, revocation, re-auth for dangerous actions; `Origin` checks against sibling subdomains; rate limits + Turnstile on email codes; security notification emails (done: passkeys, TOTP, or email codes re-authenticate, and changes to passkeys, TOTP, and recovery codes email the user; new-device emails done)
- [x] Transactional email provider on its own sending subdomain (DNS: SPF, DKIM, DMARC): **Resend**, wired in (`PANEL_RESEND_API_KEY`, `PANEL_MAIL_FROM`); `mail.raptorpanel.net` is verified (DKIM, SPF through the `send.mail` return path, DMARC at quarantine), and sending works through the SDK with a development key; the production key (sending only) goes in 1Password with the servers ([DEPLOY.md](DEPLOY.md#secrets))
- [x] Auth security review and fuzz tests (WebAuthn parsing, code verification, OAuth callbacks): fuzzers for passkey sign-in and registration, TOTP matching, email addresses, and recovery codes; the review added a per-address limit on wrong codes and ends a browser's old session when it signs in again. OAuth callbacks are covered by the fake-provider tests (login CSRF, replay, nonce, audience, PKCE) rather than fuzzing. An outside review is still wanted before launch.
- [x] Orgs, members, roles, invitations (`OrgService`; join tokens from the API, with re-auth)
- [x] Postgres RLS by `org_id` (requests for a user run as `raptor_app`; the Panel's own work as the owner)
- [x] Audit log (account activity and org logs; new-device emails)

### 3.3 Nodes
- [x] Node keys: enrollment stores the node's public key; challenge signing; Panel signing key (separate storage) pinned by Wings; revocation (refused at connect; the Panel UI to revoke comes with Phase 4)
- [ ] Join tokens; **install script** at `get.raptorpanel.net` (generated per release with the binary's SHA-256 embedded); `raptor bootstrap` preflight + setup + enroll; storage setup explains the tier 2 fsync cost and offers a data disk (tier 1) or soft limits (tier 3) (join tokens and `raptor bootstrap` done: `task e2e:bootstrap` takes fresh Debian 12, Debian 13, and Ubuntu 24.04 VMs (arm64) to linked nodes with one command, reruns without changing anything, passes doctor, and reconnects after a reboot, and the same test runs in CI on amd64 VMs of all three (`bootstrap.yml`: nightly and on PRs that touch the install path); the install script is generated per release by `task release:sign` from the verified `checksums.txt` and uploaded as `install.sh`, and the end-to-end test installs through it; `get.raptorpanel.net` serves v0.1.0's. Left: one node linked through it to production)
- [x] `raptor link` / `unlink` / `relink`
- [x] Node connections in `serve api`: connection registry, version negotiation, pings, drain, forwarding between instances via `LISTEN/NOTIFY`
- [x] Command routing to the instance holding the node, with `command_id`
- [x] Event ingestion (batched) + mirror + snapshot rebuild (servers, schedules, backups, and jobs: synced on every announcement, rebuilt from a snapshot when dropped or out of step; every change to a server's job's status is an event, and a server's 50 most recent jobs come with it)
- [ ] Node DNS: `n-<short-id>.raptornodes.net` created at enrollment, updated from the IP Wings reports, names never reused (done against a fake Cloudflare API: the record follows the address a node connects from, public addresses only; the zone and its token are set up in production, so the first real node will show whether it works; removing records with the node comes with node removal in the UI)
- [x] Signed short-lived grants attached to commands; Wings verification (Wings since 1.5; the Panel side: per-server grants for members, a permission for every Wings action, and `CommandService` sending users' commands with a grant for each)
- [x] Passkey-signed dangerous commands end to end: owner key pinned at enrollment (with fingerprint comparison), signed key additions, owner-signed delegations for sub-users (the browser signs commands and pairs keys after `raptor keys reset`; deleting a server asks for a passkey; the owner's passkey is pinned when a node links; the node page lists its trusted keys (`keys.list`) and adds, delegates, and removes them, each signed by an owner key; `TestDelegationEndToEnd` runs the chain through the Panel's API to a real Wings executor: a member's passkey approves nothing until the owner delegates, then exactly the delegated action on the delegated server, until the owner removes it)
- [x] `raptor doctor -upload` (bundles to object storage, with a support code), the Panel connection check through Cloudflare, and the node hostname check (`-upload` sends the bundle to the Panel, signed by the node key when linked, which stores it in a bucket its key can only write to and answers with a code like `RPT-7K2M-QX9D`; tested against MinIO and from the dev VM; the hostname check compares the node's DNS record with the address the Panel sees it connect from)
- [x] SFTP auth over the node connection: `PanelService.SFTPLogin` (owners and admins everything; members need `sftp` on the server and carry their file permissions). First SSH keys; now temporary per-server passwords only (DECISIONS #222, #223), checked every login
- [x] Automatic Wings updates: the Panel starts updates in stages (5% → 25% → 100%, halted if failures rise) through a `node.update` command; the health check requires the node connection to come back

**Exit criteria:** on fresh Debian 12, Debian 13, and Ubuntu 24.04 VMs (amd64 + arm64), one command links the node and it shows Connected; dropping node connections and redeploying the Panel both work without game impact; the mirror rebuilds correctly after being dropped.

---

## Phase 4 · Web app

**Goal:** the full user-facing product.

- [ ] App shell: auth flows, org switcher, navigation (a collapsible sidebar: the logo (a placeholder until the real one is in the repo, `web/src/components/logo.tsx`), the org switcher with "New org", Overview, Nodes (with each node and its connection state under it), Servers, Eggs, and Settings, how many nodes are online, and the user's menu at the bottom; it slides in from a menu button on phones; the app opens the last org you were in. A server's tabs: Overview (console and details), Files (with an SFTP button in the file manager's toolbar, a green dot while it's on, opening a dialog for a temporary username and password per server, 1 hour to 30 days), Databases (not built yet), Schedules, Backups, Network, Startup, Settings, Activity (the commands sent to it), and Access, each shown to those who may use it. Pages share one layout: a header with a back link and actions, list pages with search and a grid/list switch, and detail pages with routed tabs (a node: Overview, Settings; a server: Console, Files, Settings, Access; org settings: General, Members, Activity)), live/stale/pending/failed states (sign-in done: passkey, email code and link, OAuth buttons, the second factor, and a route guard; account settings done (`/settings`): General (name, email change confirmed by a code to the new address, the theme (system, light, or dark) synced to the account, linked accounts, details, deleting the account), Security (passkeys, the authenticator app with a locally drawn QR code and recovery codes, SSH keys, devices), Activity, and a "confirm it's you" dialog that retries the change; org settings: General (name, details, leaving), Members, Billing (what the org would pay; billing itself comes before launch), Activity; orgs done: org list and creation, members, roles, invitations (sent and accepted), the org log, nodes with their connection state, adding a node with a join token, and each node's servers with power buttons and per-member access; Turnstile on the email form done, from a separate origin so the app still loads no third-party scripts (#192), and live with its production widget; server states done: a server shows live (running), pending (installing, starting, stopping), failed (install failed, with the reason; crashed), or stale (its node is offline, so it's the node's last report), from `web/src/lib/servers.ts`; left: using them on each page below as it arrives)
- [ ] Signing prompts for dangerous actions (one signature per bulk action), trusted key and delegation management, fingerprint display at enrollment (signing for delete and pairing, trusted keys and delegations, and fingerprints at enrollment and in Security done)
- [ ] Web app hosted separately from the API on `app.raptorpanel.net`, strict CSP (kept in the repo and tested in CI), no third-party scripts, reproducible build with published bundle hashes (live on `app.raptorpanel.net` as a Cloudflare Worker, deployed by each stable release; the CSP is in the repo, `web/public/_headers`, and was checked on the production build by hand; left: a CI test, Trusted Types (the authenticator QR code is inserted as markup and needs a policy first), and published hashes)
- [ ] **Nodes:** add node (command + live enrollment progress), node list, node health page (doctor warnings), settings, remove (rename and remove done: removing refuses the node's key, removes its `raptornodes.net` records, drops its mirror and members' grants, and closes its connection on whichever instance holds it, after "confirm it's you"; its servers keep running, and relinking brings it back. Left: live enrollment progress, the health page)
- [ ] **Servers:** create wizard (egg picker filtered by arch, variables, EULA prompts, allocations), overview (status, stats graphs, players), settings, reinstall, delete (create done: `/orgs/<org>/servers/new`, from the node page's "New server": the node (with its architecture), a searchable catalog with certified games first, then the name, runtime image, required variables up front and the rest under "Game settings", a free port suggested from the node's servers, memory fitted to the node, an optional disk limit, and the EULA for eggs that need one; the user's passkey signs `server.create` with the catalog's egg in it, and the server installs and starts; the browser-built params are pinned by a vector shared with Wings' decoder. Delete was already there. Settings and reinstall done: the server page's Settings tab changes the name, runtime (signed by the passkey, as it changes what code runs), port, memory, disk limit, and the egg's variables (members see what the egg lets them), and reinstalls or wipes and reinstalls (signed) a stopped server. Left: the overview's stats graphs and players, which need live stats like the console.)
- [x] **Console:** xterm.js, one multiplexed WebSocket per tab (the server page, `/orgs/<org>/nodes/<node>/servers/<server>`: power buttons, the live console with its history, and a command line with up-arrow history; xterm loads only on that page; watching needs the new `console.read` permission, which `console.write` includes; works across Panel instances through LISTEN/NOTIFY; checked in the dev app against the Wings VM: history, live lines through a restart, and a command's output)
- [x] **Files:** browser, editor, chunked resumable uploads/downloads through the Panel (1 GB cap, "use SFTP" beyond it), archive/unarchive (browser, editor, and archives done: the server page's Files tab is a file manager: a folder tree, breadcrumbs, search, the folder and open file in the URL, a table with checkbox, Ctrl and Shift-click selection, files on the egg's denylist shown locked, new files and folders, rename, delete, compress, extract, drag-and-drop uploads, and full screen; text files up to 4 MB open in Monaco (#224) with a status bar, Ctrl/Cmd-S, drafts kept in the browser until saved, optional auto-save, and log files read-only; settings (single-click open, word wrap, sticky scroll, indent guides, autocomplete, auto-save), an editor theme, and a keyboard shortcuts list; binary files are refused. Uploads and downloads done: uploads go in 16 MB chunks to `PUT /api/files/upload`, each at the offset the node has, so a dropped connection resumes; several files upload one after another with progress and cancel; downloads are a link to `GET /api/files/download`, streamed whole from the node. Both travel on a transfer connection to the instance the browser reached (#218), checked byte for byte through the dev app against the Wings VM with a 20 MB file. Left: the per-org transfer rate limit)
- [ ] **Schedules:** builder for multi-step tasks, timezone picker, run history (the Schedules tab done: list, run now, on/off, delete, and an editor for the cron, time zone, and steps; left: a timezone picker and run history)
- [ ] **Backups:** list, create, restore, destinations, retention, key mode (the Backups tab done: list, back up now, restore, lock, delete, the dangerous ones signed; left: destinations, retention, key mode)
- [ ] **Users:** sub-users, per-server permissions
- [ ] **Egg catalog:** browse certified/community, import from URL with image + script review
- [ ] **Connection test** with provider guides
- [ ] **Subdomains** on `raptornodes.net` (A + SRV), reserved names, abuse report link (the domain is on the Public Suffix List first)
- [ ] **Audit log** view
- [ ] Performance: route code splitting, lazy xterm and editor (xterm and the editor load only when shown, each in its own chunk)

**Exit criteria:** a non-technical tester, starting from a blank VPS, gets a Minecraft server that friends can join, **without help**, in under 15 minutes.

---

## Phase 5 · Beta and launch

### Private beta
- [ ] 20–50 invited users on real boxes (mix of providers, amd64 + arm64)
- [ ] Fix-everything loop on install failures and confusing UI
- [ ] Watch: install success rate, time to first server, support tickets per user

### Launch prep
- [ ] **Server #2** and a failover drill on the real servers ([DEPLOY.md](DEPLOY.md#failover))
- [ ] **Billing (Polar):** $12/node quantity subscription (every enrolled node, any status), daily proration, first-node 1-month trial (once per org, card required), webhooks, ledger, 7-day grace, read-only mode
- [ ] **Hosted backup storage** + metering ($0.02/GB-month)
- [ ] **Support access:** request/approve/revoke flow, banners, staff console, staff MFA
- [ ] **Load test** with fake-Wings simulator (thousands of nodes)
- [ ] Status page at `status.raptorpanel.net` (hosted with a different provider than the Panel, DNS not on Cloudflare; a Better Stack page exists, but its record is on Cloudflare)
- [ ] Docs site (Astro Starlight, `docs.raptorpanel.net`): install guide, provider guides (Hetzner, OVH, Oracle Free Tier, home PC), CLI reference, egg authoring
- [ ] AGPL source link in the Panel footer (deployed commit)

**Exit criteria (launch gate):**
- Install success rate ≥ 95% on supported OSes during beta
- No known critical/high security issues
- Postgres failover tested
- Billing tested end to end, including failed payments and read-only mode

---

## Business and legal track (parallel, ongoing)

| When | Task |
|---|---|
| Now | Reserve GitHub org, social handles, Discord server |
| Now | Landing page + waitlist on `raptorpanel.net` (Astro, `site/`) |
| Now | Register `raptorpanel.com` (and `raptornodes.com` if available) and redirect them |
| By Phase 4 | Apply to add `raptornodes.net` to the Public Suffix List (takes weeks; must be done before user subdomains) |
| By Phase 3 | Business entity (needed for Polar payouts) |
| By Phase 3 | `security@raptorpanel.net` mailbox |
| By Phase 5 | Terms of Service, Privacy Policy, DPA (covering support access and player data) |
| By Phase 5 | Polar account verified; products and trial configured |
| Ongoing | Share progress publicly (devlogs, Discord) to build the beta list |

---

## After launch (unordered)

- Server transfers between nodes (direct node-to-node)
- Mod/plugin manager (Modrinth, CurseForge, Steam Workshop)
- Connection relay for CGNAT users (paid add-on)
- Queued config edits for offline nodes
- Public API keys + webhooks
- More certified games and OS versions
- End-to-end encryption of node traffic inside the WebSocket (so Cloudflare can't read it), if customers ask for it

## Metrics to watch from beta onward

| Metric | Why |
|---|---|
| Install success rate (by OS/provider) | Onboarding is the product's first impression |
| Time from signup to first running server | Core UX promise |
| Trial → paid conversion, **by box size** | Tests whether $12/node works for small VPS owners |
| Support tickets per node | Where docs, `doctor`, or UX are failing |
| Game downtime attributable to Raptor | Must stay at 0 |
