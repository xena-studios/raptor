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

**Critical path:** egg runtime → Wings core → tunnel + enrollment → web app → billing. The egg runtime is the biggest risk: if it takes longer, everything after it moves.

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
- [ ] Generate the release key and commit `release/minisign.pub` (maintainer, offline)
- (The per-release install script with the embedded SHA-256 moves to Phase 3.3, alongside the install script itself.)

### Repository
- [ ] Branch protection on `main`: require PRs and passing CI

**Exit criteria:** `git push` runs all checks green; a tagged release produces signed binaries for both architectures.

---

## Phase 1 · Wings core

**Goal:** a Wings daemon that runs egg-based servers reliably. No Panel yet; driven by a test harness and the CLI.

### 1.0 Validation gate: egg runtime
- [ ] Read Pterodactyl Wings' source: environment building, container config, install process, stop handling, config parsers
- [ ] Write down the exact env var list, container UID/GID, mounts, and entrypoint behavior in [EGGS.md](EGGS.md)
- [ ] Get **Paper**, **Rust**, and a **Node.js Discord bot** egg installing and running with unmodified yolks images
- **Gate:** all three run. This becomes the first real code of the egg engine, not throwaway.

### 1.1 Daemon skeleton
- [ ] `raptor wings run`: config loading, `slog` logging, graceful shutdown
- [ ] SQLite: WAL, single writer + readers, migrations, `VACUUM INTO` snapshots
- [ ] Local socket API (`/run/raptor/wings.sock`, root + `raptor` group)
- [ ] systemd unit (`OOMScoreAdjust=-900`, sandboxing options)

### 1.2 Runtime
- [ ] `Runtime` interface + Docker implementation
- [ ] `raptor_nw` network with collision-free subnet selection
- [ ] Labels `raptor.wings.*`; every query filtered by `raptor.wings.managed=true`
- [ ] Resource limits: memory + overhead, CPU weight (default) / hard limit / pinning, PID limit, `raptor.slice`
- [ ] Hardening: non-root, cap drop, `no-new-privileges`, seccomp, only the server dir mounted
- [ ] Port publishing for allocations; host-port-in-use check; optional host networking
- [ ] `RAPTOR` firewall chain + jump from `DOCKER-USER`

### 1.3 Egg engine
- [ ] Parsers for `PTDL_v1`, `PTDL_v2`, `PLCN_v*`; reject unknown versions
- [ ] Variables: Laravel-style rule validation (the subset real eggs use), validated **before** substitution
- [ ] Install containers (`/mnt/server`), install logs
- [ ] Runtime environment matching Pterodactyl Wings exactly (from 1.0)
- [ ] Startup "done" detection, stop commands and signals, timeouts
- [ ] Config file parsers: properties, yaml, json, ini, xml, file, all through `os.Root`
- [ ] `x-raptor` extension parsing
- [ ] Arch check against image manifests
- [ ] Fuzz tests: variable substitution + config parsers + path handling

### 1.4 Server lifecycle
- [ ] Create / install / reinstall / delete / start / stop / restart / kill
- [ ] Console: stdout drained into a ring buffer, stdin commands, never blocks the container
- [ ] **Reattach on Wings start**: reconcile containers against SQLite, refill console from Docker logs
- [ ] Crash detection + restart with backoff + crash-loop stop

### 1.5 Job engine
- [ ] Durable queue in SQLite, resume/retry, per-server locks, global concurrency limits
- [ ] Job logs
- [ ] Event outbox with monotonic `seq`
- [ ] `executed_commands` table for idempotency

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
- [ ] **SFTP:** `x/crypto/ssh`, `user.serverid`, `os.Root` chroot, host key, auth callback interface (stubbed), public key cache
- [ ] **HTTPS file server:** port 8443, signed short-lived tokens, streaming upload/download
- [ ] **Notifications:** Discord + generic webhooks from Wings
- [ ] **Local metrics:** ~7 days, downsampled
- [ ] **`doctor`:** all checks from [WINGS.md](WINGS.md#doctor), with fix messages; `--bundle`, `--upload`
- [ ] **TUI** (Bubble Tea): server list, stats, console, power + backup keys
- [ ] **Self-update:** channels, signature verification, atomic swap, rollback on failure
- [ ] **Pterodactyl coexistence** CI test + **`raptor import pterodactyl`**
- [ ] **Host disk protection:** refuse installs/pulls below the threshold
- [ ] **Fault-injection suite:** kill tunnel/Wings/Docker mid-job, reboot, fill disks, unmount the volume, corrupt `state.db`

**Exit criteria:** fault-injection suite passes; a node survives a week-long soak test (scheduled restarts + backups + random Wings restarts) with no unexpected game downtime.

---

## Phase 3 · Panel core

**Goal:** a real Panel that nodes connect to. Minimal UI.

### 3.0 Validation gate: tunnel
- [ ] Wings dials the Panel over mTLS + yamux; RPCs in both directions
- [ ] Kill the connection mid-RPC → reconnect with jitter → retry with the same `command_id` → no duplicate execution
- [ ] Stream 10 MB on one stream while measuring console latency on another
- **Gate:** RPCs work both ways, retries are idempotent, console latency stays acceptable during the transfer. This becomes the real tunnel code.

### 3.1 Infrastructure
- [ ] Server #1 (primary) + #2 (Postgres replica); Docker Compose; Caddy
- [ ] pgBackRest WAL archive to object storage in another location; **restore test**
- [ ] DNS for `raptorpanel.net` and `raptornodes.net`; `tunnel.raptorpanel.net` unproxied
- [ ] Observability: OpenTelemetry → Grafana (Cloud or self-hosted), alerts
- [ ] Deploy pipeline: zero-downtime `api` deploys; graceful `tunnel` drain

### 3.2 Accounts
- [ ] WorkOS AuthKit integration; own `users` table; Panel-issued sessions; CSRF
- [ ] Orgs, members, roles, invitations
- [ ] Postgres RLS by `org_id`
- [ ] Audit log

### 3.3 Nodes
- [ ] Internal CA (separate key storage); client cert issuance + rotation
- [ ] Join tokens; **install script** at `get.raptorpanel.net` (generated per release with the binary's SHA-256 embedded); `raptor bootstrap` preflight + setup + enroll
- [ ] `raptor link` / `unlink` / `relink`
- [ ] Tunnel role: connection registry, version negotiation, heartbeats, drain
- [ ] Command routing api → tunnel → Wings, with `command_id`
- [ ] Event ingestion (batched) + mirror + snapshot rebuild
- [ ] Node certs for `<node-id>.node.raptornodes.net` via Let's Encrypt DNS-01, delivered over the tunnel
- [ ] Signed short-lived grants attached to commands; Wings verification
- [ ] SFTP auth over tunnel + public key sync

**Exit criteria:** on fresh Debian 12, Debian 13, and Ubuntu 24.04 VMs (amd64 + arm64), one command links the node and it shows Connected; killing the tunnel process and redeploying `api` both work without game impact; the mirror rebuilds correctly after being dropped.

---

## Phase 4 · Web app

**Goal:** the full user-facing product.

- [ ] App shell: auth flows, org switcher, navigation, live/stale/pending/failed states
- [ ] **Nodes:** add node (command + live enrollment progress), node list, node health page (doctor warnings), settings, remove
- [ ] **Servers:** create wizard (egg picker filtered by arch, variables, EULA prompts, allocations), overview (status, stats graphs, players), settings, reinstall, delete
- [ ] **Console:** xterm.js, one multiplexed WebSocket per tab
- [ ] **Files:** browser, Monaco editor, direct large uploads/downloads to `:8443`, archive/unarchive
- [ ] **Schedules:** builder for multi-step tasks, timezone picker, run history
- [ ] **Backups:** list, create, restore, destinations, retention, key mode
- [ ] **Users:** sub-users, per-server permissions, SSH keys
- [ ] **Egg catalog:** browse certified/community, import from URL with image + script review
- [ ] **Connection test** with provider guides
- [ ] **Subdomains** on `raptornodes.net` (A + SRV), reserved names, abuse report link
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
- [ ] Status page (hosted with a different provider than the Panel)
- [ ] Docs site: install guide, provider guides (Hetzner, OVH, Oracle Free Tier, home PC), CLI reference, egg authoring
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
| Now | Landing page + waitlist on `raptorpanel.net` |
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
- QUIC tunnel, if measurements call for it

## Metrics to watch from beta onward

| Metric | Why |
|---|---|
| Install success rate (by OS/provider) | Onboarding is the product's first impression |
| Time from signup to first running server | Core UX promise |
| Trial → paid conversion, **by box size** | Tests whether $12/node works for small VPS owners |
| Support tickets per node | Where docs, `doctor`, or UX are failing |
| Game downtime attributable to Raptor | Must stay at 0 |
