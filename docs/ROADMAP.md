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
- [x] Install containers per [EGGS.md](EGGS.md#install): `/mnt/server` + read-only `/mnt/install`, hardening, limits, timeout, `raptor_install` network isolation, symlink-safe ownership fix, capped logs, failure behavior. "Wipe and reinstall" needs backups and moves to Phase 2.
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
- [x] Deletion: file cleanup and freed allocations. The final backup and orphaned offsite backups come with backups (Phase 2), quota cleanup with 1.6.
- [x] `task e2e:host`: Wings restart, Docker restart, host shutdown, and reboot, against the real systemd units

### 1.5 Job engine
- [ ] Durable queue in SQLite, resume/retry, per-server locks, global concurrency limits
- [ ] Job logs
- [ ] Event outbox with monotonic `seq`
- [ ] `executed_commands` table for idempotency, also used as replay protection for signed commands
- [ ] **Command envelope:** `command_id`, expiry, Panel grant, and optional passkey signature (`authenticatorData`, `clientDataJSON`, signature); RFC 8785 canonical form; which actions require a signature ([SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands))
- [ ] **Signed-command verification in Wings:** trusted keys and delegations in SQLite, the full WebAuthn assertion check, rejection and logging of anything unsigned or invalid; tested with a software authenticator and fuzzed

### 1.6 Disk quotas
- [ ] **Validation gate first:** on a fresh Debian 12 VM (ext4 root), loop image + systemd mount unit + project quotas set from Go. Fill past the limit, reboot, grow online, and benchmark against native disk. **Gate:** limits hold, survive reboot, grow online, within ~10% of native I/O.
- [ ] Quota volume tiers 1–3
- [ ] Refuse to start servers if the volume isn't mounted
- [ ] Instant usage reporting

### 1.7 CLI (first cut)
- [ ] `status`, `ps`, `start|stop|restart|kill`, `console`, `logs`

### 1.8 Egg conformance suite
- [ ] Harness: import → install → start → done → command → stop → reinstall
- [ ] Certified eggs + top ~50 community eggs, running in CI on a VM
- [ ] Behavioral diff test against Pterodactyl Wings for a subset

**Exit criteria:**
- Paper, Rust, Hytale, and a Discord bot install and run from the harness
- `systemctl restart raptor-wings` → **no game server restarts**, console history intact
- `systemctl restart docker` → servers keep running (`live-restore`)
- Conformance suite passes for all certified eggs
- A symlink/path-traversal test suite passes

---

## Phase 2 · Wings complete

**Goal:** every node-side feature exists, tested against a stub Panel.

- [ ] **Scheduler:** cron + timezone, multi-step tasks, `only_when_online`, jitter, missed-run policy
- [ ] **Backups (Kopia):** local + S3 destinations, egg pre/post hooks, retention + prune jobs, safety backup before restore, low CPU/I/O weight, on by default
- [ ] **SFTP:** off by default, enabled per node; `x/crypto/ssh`, `user.serverid`, `os.Root` chroot, host key generated on the node, auth callback interface (stubbed), public key cache
- [ ] **File operations for the web file manager:** list, read, write, rename, delete, archive, through `os.Root`; chunked, resumable uploads and downloads (chunks under 100 MB, 1 GB per-file cap) on a separate outbound connection per transfer. No HTTP server on the node.
- [ ] **Notifications:** Discord + generic webhooks from Wings, including direct alerts for every signed dangerous action (configured on the node, not through the Panel)
- [ ] **`raptor keys list|reset`** (pairing code shown on the box) and **`raptor audit`**
- [ ] **Local metrics:** ~7 days, downsampled
- [ ] **`doctor`:** all checks from [WINGS.md](WINGS.md#doctor), with fix messages; `--bundle`, `--upload`
- [ ] **TUI** (Bubble Tea): server list, stats, console, power + backup keys
- [ ] **Self-update:** channels, signature verification, atomic swap, rollback on failure
- [ ] **Pterodactyl coexistence** CI test + **`raptor import pterodactyl`**
- [ ] **Host disk protection:** refuse installs/pulls below the threshold
- [ ] **Fault-injection suite:** drop the node connection, kill Wings/Docker mid-job, reboot, fill disks, unmount the volume, corrupt `state.db`

**Exit criteria:** fault-injection suite passes; a node survives a week-long soak test (scheduled restarts + backups + random Wings restarts) with no unexpected game downtime.

---

## Phase 3 · Panel core

**Goal:** a real Panel that nodes connect to. Minimal UI.

### 3.0 Validation gate: node connection through Cloudflare
- [ ] Wings dials `wss://` through the real Cloudflare proxy; yamux inside; RPCs in both directions; challenge signatures both ways
- [ ] Kill the connection mid-RPC → reconnect with jitter → retry with the same `command_id` → no duplicate execution
- [ ] Hold connections idle for hours (ping keeps them alive), survive Cloudflare dropping them, and measure console latency while a 1 GB upload runs on its separate transfer connection
- **Gate:** RPCs work both ways through Cloudflare, retries are idempotent, idle connections stay up, console latency stays acceptable during a transfer. This becomes the real connection code.

### 3.1 Infrastructure
- [ ] Server #1 (primary) + #2 (Postgres replica); Docker Compose; Caddy
- [ ] pgBackRest WAL archive to object storage in another location; **restore test**
- [ ] DNS: hostnames from [ARCHITECTURE.md](ARCHITECTURE.md#hostnames); `api.raptorpanel.net` behind the Cloudflare proxy (origin locked to Cloudflare, WAF rule for node connections); `raptornodes.net` DNS-only on a plan with enough records, apex redirecting to `raptorpanel.net`
- [ ] Domain account hardening: hardware-key 2FA on the registrar and Cloudflare (no SMS recovery), registrar lock, scoped API tokens; decide whether the web app is hosted under a separate account or provider from DNS and the proxy
- [ ] Move code to the split hostnames: API on `api.raptorpanel.net` with CORS for `https://app.raptorpanel.net` only, node connections at `wss://api.raptorpanel.net/nodes/connect`, Wings' default Panel URL, WebAuthn origin and RP ID `app.raptorpanel.net`
- [ ] Observability: OpenTelemetry → Grafana (Cloud or self-hosted), alerts
- [ ] Deploy pipeline: zero-downtime deploys, with node connections drained and reconnected with jitter

### 3.2 Accounts
- [ ] Passwordless auth in the Panel ([PANEL.md](PANEL.md#auth)): passkeys, OAuth (Google, Discord, GitHub), email codes + links, TOTP + recovery codes, safe OAuth account linking
- [ ] Sessions: hashed tokens in a `__Host-` cookie on `api.`, device list, revocation, re-auth for dangerous actions; `Origin` checks against sibling subdomains; rate limits + Turnstile on email codes; security notification emails
- [ ] Transactional email provider on its own sending subdomain (DNS: SPF, DKIM, DMARC)
- [ ] Auth security review and fuzz tests (WebAuthn parsing, code verification, OAuth callbacks)
- [ ] Orgs, members, roles, invitations
- [ ] Postgres RLS by `org_id`
- [ ] Audit log

### 3.3 Nodes
- [ ] Node keys: enrollment stores the node's public key; challenge signing; Panel signing key (separate storage) pinned by Wings; revocation
- [ ] Join tokens; **install script** at `get.raptorpanel.net` (generated per release with the binary's SHA-256 embedded); `raptor bootstrap` preflight + setup + enroll
- [ ] `raptor link` / `unlink` / `relink`
- [ ] Node connections in `serve api`: connection registry, version negotiation, pings, drain, forwarding between instances via `LISTEN/NOTIFY`
- [ ] Command routing to the instance holding the node, with `command_id`
- [ ] Event ingestion (batched) + mirror + snapshot rebuild
- [ ] Node DNS: `n-<short-id>.raptornodes.net` created at enrollment, updated from the IP Wings reports, names never reused
- [ ] Signed short-lived grants attached to commands; Wings verification
- [ ] Passkey-signed dangerous commands end to end: owner key pinned at enrollment (with fingerprint comparison), signed key additions, owner-signed delegations for sub-users
- [ ] SFTP auth over the node connection + public key sync

**Exit criteria:** on fresh Debian 12, Debian 13, and Ubuntu 24.04 VMs (amd64 + arm64), one command links the node and it shows Connected; dropping node connections and redeploying the Panel both work without game impact; the mirror rebuilds correctly after being dropped.

---

## Phase 4 · Web app

**Goal:** the full user-facing product.

- [ ] App shell: auth flows, org switcher, navigation, live/stale/pending/failed states
- [ ] Signing prompts for dangerous actions (one signature per bulk action), trusted key and delegation management, fingerprint display at enrollment
- [ ] Web app hosted separately from the API on `app.raptorpanel.net`, strict CSP (kept in the repo and tested in CI), no third-party scripts, reproducible build with published bundle hashes
- [ ] **Nodes:** add node (command + live enrollment progress), node list, node health page (doctor warnings), settings, remove
- [ ] **Servers:** create wizard (egg picker filtered by arch, variables, EULA prompts, allocations), overview (status, stats graphs, players), settings, reinstall, delete
- [ ] **Console:** xterm.js, one multiplexed WebSocket per tab
- [ ] **Files:** browser, Monaco editor, chunked resumable uploads/downloads through the Panel (1 GB cap, "use SFTP" beyond it), archive/unarchive
- [ ] **Schedules:** builder for multi-step tasks, timezone picker, run history
- [ ] **Backups:** list, create, restore, destinations, retention, key mode
- [ ] **Users:** sub-users, per-server permissions, SSH keys
- [ ] **Egg catalog:** browse certified/community, import from URL with image + script review
- [ ] **Connection test** with provider guides
- [ ] **Subdomains** on `raptornodes.net` (A + SRV), reserved names, abuse report link (the domain is on the Public Suffix List first)
- [ ] **Audit log** view
- [ ] Performance: route code splitting, lazy xterm/Monaco

**Exit criteria:** a non-technical tester, starting from a blank VPS, gets a Minecraft server that friends can join, **without help**, in under 15 minutes.

---

## Phase 5 · Beta and launch

### Private beta
- [ ] 20–50 invited users on real boxes (mix of providers, amd64 + arm64)
- [ ] Fix-everything loop on install failures and confusing UI
- [ ] Watch: install success rate, time to first server, support tickets per user

### Launch prep
- [ ] **Billing (Polar):** $12/node quantity subscription (every enrolled node, any status), daily proration, first-node 1-month trial (once per org, card required), webhooks, ledger, 7-day grace, read-only mode
- [ ] **Hosted backup storage** + metering ($0.02/GB-month)
- [ ] **Support access:** request/approve/revoke flow, banners, staff console, staff MFA
- [ ] **Load test** with fake-Wings simulator (thousands of nodes)
- [ ] Status page at `status.raptorpanel.net` (hosted with a different provider than the Panel, DNS not on Cloudflare)
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
