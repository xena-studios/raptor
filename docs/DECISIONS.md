# Decisions Log

Locked decisions and the reasons behind them. Change a decision by adding a new entry that supersedes the old one. Don't edit history.

| # | Decision | Why |
|---|---|---|
| 1 | **Product:** hosted panel; owners bring their own Linux boxes | SaaS with no hardware costs. Owners keep control of their machines. |
| 2 | **Audience:** server owners and people with boxes who lack a reliable way to host. **Not** hosting companies. | Hosting companies need white-label, billing integrations, and reseller features. That's a different product. |
| 3 | **Scope:** games first. Discord bots, voice servers, and generic Node/Python apps officially supported via eggs. Not a general app platform. | Communities run bots and voice alongside game servers, and eggs make it nearly free. General app hosting is Coolify/Dokploy's market. |
| 4 | **Names:** Raptor · **Panel** + **Wings** · CLI `raptor` | Pterodactyl-style naming. Wings is kept despite Pterodactyl's Wings, with full namespacing so they never collide on a box (#26). |
| 5 | **Domains:** `raptorpanel.net` (Panel, API, installer, tunnel); `raptornodes.net` (player subdomains like `smp.raptornodes.net`); `node.raptornodes.net` (node hostnames + certs, `<node-id>.node.raptornodes.net`) | Keeps node infrastructure and player-facing names off the Panel's domain. The `node.` label keeps player names from ever colliding with node hostnames. |
| 6 | **License:** AGPL-3.0 for everything | Fully open source. Competitors are acceptable; forks must publish their changes. |
| 7 | **Contributions:** DCO, no CLA | Less friction, more contributor trust. Relicensing rights aren't needed because revenue comes from hosting, not licenses. |
| 8 | **Panel is SaaS-only.** No self-hosting support or concessions in the stack. | Keeps the codebase to one deployment target. |
| 9 | **Languages:** Go (all backend + Wings), TypeScript + React (web) | Go: single static binaries, strong concurrency, first-class Docker/containerd libraries. |
| 10 | **Contracts:** protobuf + Connect-RPC | One source of truth for Go and TS types. |
| 11 | **Panel:** one Go binary, two process roles (`api`, `tunnel`); Postgres only (River jobs, LISTEN/NOTIFY) | Fewest moving parts. Separate roles so API deploys don't drop every node. |
| 12 | **Hosting:** two servers (primary + Postgres streaming replica), Docker Compose, WAL archive in a different location. Provider intentionally not fixed in the docs. | Simple to run. Replica because Postgres is the Panel's single point of failure. Provider may change. |
| 13 | **Auth:** WorkOS; the Panel issues its own sessions; own `users` table | Good hosted auth. Own sessions survive WorkOS outages. Own IDs make switching providers possible. |
| 14 | **Billing:** Polar (merchant of record). **$12/month for every enrolled node, regardless of status**; per-unit quantity subscription, prorated daily. First node free for one month, once per org, card required. Hosted backups $0.02/GB-month. | Polar handles global VAT and sales tax. Bills only on things Raptor can verify. Card-up-front trial blocks re-trial abuse. |
| 15 | **Non-payment never stops servers.** The Panel goes read-only for unpaid nodes. | Trust. The hardware is the owner's. |
| 16 | **Onboarding:** one command; join token; keys generated on the node; mTLS | The core UX promise. |
| 17 | **Wings dials out** over mTLS + yamux; Panel is an RPC client over it | No inbound management port, works behind NAT, one connection per node. |
| 18 | **Wings is local-first** and the source of truth for servers, schedules, backups, jobs, files | Servers survive Panel outages. |
| 19 | **Single writer:** the Panel sends commands; Wings owns state; the Panel keeps a disposable read-only mirror | No merge conflicts. The mirror keeps the UI fast and makes dead-box recovery possible. |
| 20 | **CLI scope:** keep servers running (status, power, console, logs, backups, doctor). No config changes. | Config only comes from the Panel (#19). Smaller API surface. |
| 21 | **No standalone Wings product.** Wings must be linked to be configured. | Follows from #20. Avoids a second product mode to test. |
| 22 | **Container runtime:** Docker behind a `Runtime` interface; `live-restore`, `userland-proxy: false`, `local` log driver, package held | Egg compatibility requires Docker images. Settings chosen for uptime and UDP performance. |
| 23 | **Docker firewall:** leave Docker's iptables on; Wings publishes only allocated ports; own rules in a `RAPTOR` chain | Disabling Docker's iptables would break networking for other containers on the box. |
| 24 | **Disk quotas:** XFS project quotas; XFS loop image (preallocated, direct I/O) on non-XFS hosts; soft limits only on opt-out | Kernel-enforced, zero overhead, instant usage readout. |
| 25 | **Eggs:** Pterodactyl (`PTDL_v1/v2`) and Pelican formats run natively; Raptor extras under `x-raptor`; Pterodactyl Wings' runtime behavior is the spec | Instant catalog. Extensions don't break other panels. |
| 26 | **Coexistence:** every on-box resource namespaced; labels `raptor.wings.*`; Wings never mentions the Panel's name or domain in code | Runs alongside Pterodactyl's Wings. Wings stays a cleanly separated program. |
| 27 | **Backups:** Kopia; on by default; Panel-held per-node key by default, owner-held optional | restic can't be embedded as a library. Panel-held key means backups survive a dead box. |
| 28 | **Files:** small ops via tunnel (≤ 10 MB); large transfers and SFTP go directly to the node; node certs issued by the Panel via DNS-01 on `raptornodes.net` | Bandwidth costs and speed. No port 80 needed on nodes. |
| 29 | **SFTP during Panel outage:** Wings caches users' SSH public keys and permissions; no password caching | Public keys aren't secrets. |
| 30 | **Support:** consent-based, scoped (3 levels), time-limited, audited; no shell for staff; `doctor --bundle` only as the offline fallback | Live access for connected nodes; bundle covers nodes that can't connect. |
| 31 | **Launch OS:** Debian 12, Debian 13, Ubuntu 24.04; amd64 + arm64 | Keeps the test matrix manageable. Add versions on demand. |
| 32 | **Certified games:** Minecraft family, Hytale, Rust, and selected anonymous-SteamCMD games; x86-only games hidden on arm64 | See [EGGS.md](EGGS.md). |
| 33 | **Beginner features in v1:** connection test, free subdomains, safe defaults, security warnings (never automatic changes), provider guides | The audience isn't Linux experts. |
| 34 | **CPU limits default to weight, not hard CFS quotas** | Hard quotas cause tick lag in game servers. |
| 35 | **Wings updates:** signed, staged, automatic rollback | A bad release must not strand nodes. |
| 36 | **Release signing:** minisign (Ed25519) over `checksums.txt`. CI (GoReleaser) builds a **draft** release; the maintainer signs locally with the offline key (`task release:sign`) and publishes. The install script is generated per release with the binary's SHA-256 embedded; the `raptor` binary verifies later updates (`checksums.txt.minisig`, then SHA-256) with an embedded public key. | Simple for single binaries. The private key never touches CI. Avoids needing a verifier installed on a fresh box. |
| 37 | **Node ports:** SFTP 2022, falling back to the first free port in 2022–2099; HTTPS files 8443, falling back within 8443–8499. The Panel always shows the real port. | Coexists with Pterodactyl (which uses 2022) without manual setup. |
| 38 | **No spike phase.** Riskiest assumptions (egg runtime, XFS quotas, tunnel) are validated as the first tasks of the phase that needs them. | Owner's call. The first task in each phase is a validation gate instead. |
| 39 | **Dev tools pinned in `tools/go.mod`** and run via `go tool -modfile=tools/go.mod` (buf, sqlc, protoc plugins, golangci-lint, govulncheck, go-licenses). gitleaks runs from its pinned Docker image. | Same versions locally and in CI, nothing to install globally, and tool dependencies stay out of the shipped binaries. gitleaks' dependencies conflict with golangci-lint's in a shared module. |
| 40 | **Biome** for web lint + format (not ESLint/Prettier) | The web app uses TypeScript 7 (native compiler), which typescript-eslint doesn't support. Biome is one fast tool with no TypeScript dependency. |
| 41 | **Generated code is committed** (Go protobuf, sqlc, TS protobuf, route tree); CI fails if it's stale | The project builds with plain `go build` / `pnpm build`, and generated changes show up in review. |
| 42 | **Taskfile is the single entry point** for dev and CI (`task check` = everything CI runs) | CI and local runs can't drift apart. |
| 43 | **Wings dev VM:** Lima, Debian 12, Docker CE from Docker's repository | Wings is Linux-only. Docker CE matches what the installer will use; Debian's `docker.io` is years behind. |
| 44 | **Install containers are isolated as untrusted code:** separate short-lived container, only the server dir (rw) and script (ro) mounted, never privileged, limits + 2h default timeout, own network with outbound-internet-only access (host, private ranges, other servers, and cloud metadata blocked), symlink-safe `lchown` afterwards | Egg install scripts run as root with internet access and come from third parties. Pterodactyl-compatible behavior is kept (root, `/mnt/server`, reinstall over existing files); the isolation is added around it. |
| 45 | **Servers store an egg snapshot**, not a live reference | Catalog updates must never silently change a running server. Updating the egg is an explicit action. |
| 46 | **Wings, not Docker, restarts servers** (restart policy `no`, `desired_state` reconciled on start) | Wings must verify the quota volume is mounted before starting anything; Docker's restart policy would bypass that. |
| 47 | **Graceful stop on host shutdown** via `raptor-shutdown.service`, plus Docker `shutdown-timeout: 90` | Docker's default short stop timeout can corrupt worlds that are still saving. |
| 48 | **Crash policy:** exit 0 counts as a crash by default (Pterodactyl-compatible); restarts at 0/10/30/60 s; 3 crashes in 10 min = crash loop, stop and notify | Predictable, matches Pterodactyl, and avoids infinite restart loops. |
| 49 | **Console:** always drained, 1,000-line ring buffer, 1,000 lines/s streamed to viewers with a suppression marker, never kill a server for spam | A chatty or broken server can't stall Wings, browsers, or itself. |
| 50 | **Deleting a server keeps offsite backups** (orphaned, 30-day expiry for hosted); local backups are deleted | Deletion is irreversible; offsite backups are the only way back. |
| 51 | **`config.yml` holds only static box settings**, strict parsing; everything else lives in SQLite from the Panel | One source of truth for server config; typos in the file fail loudly. |
