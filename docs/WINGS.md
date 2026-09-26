# Wings

Wings is the daemon that runs on the owner's box. It is the **source of truth** for everything on that box, and it keeps working without the Panel.

## Principles

1. **Local-first.** Once configured, Wings needs nothing from the Panel to keep servers running, schedules firing, and backups happening.
2. **Wings restarts never stop servers.** Containers belong to Docker, not to the Wings process. Wings crashing, restarting, or updating causes zero game downtime.
3. **Typed commands only.** Wings exposes specific operations. It never executes arbitrary shell commands from the Panel, the CLI, or support.
4. **The host boundary is sacred.** Anything inside a container is contained. Anything Wings does on the host with server-controlled data (paths, symlinks, config files) is treated as hostile input.
5. **Neutral naming.** Wings never hardcodes the Panel's name or domain. The Panel URL, update channel, and CA come from config written by the installer.
6. **Good neighbor.** Wings coexists with Pterodactyl's Wings and anything else on the box. It only touches resources it created.

## Binary and processes

One static Go binary, `/usr/local/bin/raptor` (no CGO; SQLite via `modernc.org/sqlite`).

| Command | What |
|---|---|
| `raptor wings run` | The daemon (started by `raptor-wings.service`) |
| `raptor <cmd>` | CLI, talks to the daemon over `/run/raptor/wings.sock` |
| `raptor tui` | Terminal UI |
| `raptor bootstrap` | Install/enroll (called by the install script) |

## On-box footprint

| Resource | Value |
|---|---|
| Binary | `/usr/local/bin/raptor` |
| Services | `raptor-wings.service`; `raptor-shutdown.service` (graceful stops on host shutdown) |
| systemd drop-in | `/etc/systemd/system/docker-.scope.d/10-raptor.conf`: container scopes stop after `raptor-shutdown` ([SERVERS.md](SERVERS.md#host-shutdown)) |
| Config | `/etc/raptor/config.yml` |
| State DB | `/var/lib/raptor/state.db` |
| Server data | `/var/lib/raptor/volumes/<server-id>/` (quota volume) |
| Logs | `/var/log/raptor/` (install logs in `install/<server-id>.log`) |
| Socket | `/run/raptor/wings.sock` (root + `raptor` group) |
| System user | `raptor` |
| Docker networks | `raptor_nw` (bridge `raptor0`) for servers, `raptor_install` (bridge `raptor-inst`) for installs; subnets picked automatically to avoid collisions |
| cgroup | `raptor.slice` (all Raptor containers) |
| Container labels | `raptor.wings.managed=true`, `raptor.wings.server=<id>`, `raptor.wings.node=<id>` (once linked), `raptor.wings.role=server\|install`. Networks carry `raptor.wings.managed=true`. |
| Firewall | nftables table `inet raptor` (see [Firewall](#firewall)) |

### Coexistence with Pterodactyl's Wings
- Different binary, service, paths, network, subnet, labels, and user. No shared resources.
- Wings **only lists and manages containers labeled `raptor.wings.managed=true`**. It never prunes or touches others. If a container or network with one of Wings' names exists without the label, Wings refuses to touch it and reports an error instead.
- Before accepting a port allocation, Wings checks that the host port is free. The Panel rejects allocations Wings reports as taken.
- SFTP (only when enabled) uses 2022 unless it's taken (Pterodactyl's default), then the first free port in 2022–2099. The Panel always shows the real port. Wings runs no HTTP server.
- The installer **merges** into `/etc/docker/daemon.json`, never overwrites it.
- A CI test runs both Wings on one VM and checks neither disturbs the other.

## systemd service

`install/systemd/raptor-wings.service` (installed by `raptor bootstrap`; in development by `task wings:vm:install`):
- `Restart=always`, `OOMScoreAdjust=-900`, `LimitNOFILE=65536`.
- **Stopping or restarting the service never stops servers:** their containers live in Docker's cgroups, not the service's.
- `UMask=0077`, and systemd creates `/var/lib/raptor`, `/var/log/raptor`, and `/etc/raptor` as `0700`. `/run/raptor` is `0755` so the `raptor` group can reach the socket.
- Sandboxing: `ProtectSystem=strict` (writable: `/etc/raptor`, `/var/lib/raptor`, `/var/log/raptor`, `/run/raptor`), `ProtectHome`, `PrivateTmp`, `NoNewPrivileges`, kernel module/log/clock/hostname/cgroup protection, `RestrictNamespaces`, `RestrictSUIDSGID`, native syscalls only, and only `AF_UNIX`/`AF_INET`/`AF_INET6`/`AF_NETLINK` sockets. `systemd-analyze security` rates it 5.9 (medium). Wings runs `nft` and `systemctl` itself (firewall and slice setup) and both work inside this sandbox. Wings needs root for Docker, quotas, and nftables, so it can't go much lower; this will be revisited as those features land.
- On shutdown Wings stops the local API (the socket is removed), detaches from servers (they keep running), and closes the database.
- When the runtime is ready, Wings starts the server manager, which reconciles with Docker: it reattaches to running servers and starts those that should run ([SERVERS.md](SERVERS.md#after-a-reboot-or-wings-restart)). Server containers run as the `raptor` user's UID/GID, with the host's time zone as `TZ`.

## Config file

`/etc/raptor/config.yml` holds **only static, box-level settings**, written by the installer and rarely changed. Everything about servers, schedules, and backups lives in SQLite and comes from the Panel. Owned by root, mode `0600`.

```yaml
node_id: 0192f0a4-...            # assigned at enrollment
panel:
  url: https://raptorpanel.net   # never hardcoded in Wings
identity:                        # written at enrollment (Phase 3), root-only 0600
  key: /etc/raptor/node.key      # the node's private key; generated on the box, never leaves it
  panel_key: /etc/raptor/panel.pub  # the Panel's signing key, pinned at enrollment
paths:
  state: /var/lib/raptor/state.db
  volumes: /var/lib/raptor/volumes
  tmp: /var/lib/raptor/tmp
  logs: /var/log/raptor
  socket: /run/raptor/wings.sock
docker:
  network: raptor_nw
  subnet: ""                     # empty: first free range, picked when the network is created
  install_network: raptor_install
  install_subnet: ""
  install_allow: []              # private CIDRs installs may reach, e.g. [192.168.1.10/32]
ports:
  sftp: 2022
limits:
  concurrent_installs: 2
  concurrent_backups: 2
  host_disk_min_free: 10GiB      # below this: refuse installs and image pulls
  reserved_memory: 0             # kept free of servers; 0 = 10% of RAM, 1–4 GiB
updates:
  channel: stable
  pin: ""                        # e.g. "1.4.2" to pin a version
log:
  level: info
```

- Unknown keys are an error, so typos don't go unnoticed.
- `raptor doctor` validates the file.
- Changes need `systemctl restart raptor-wings` (servers are unaffected).

## Local state (SQLite)

- **WAL mode**, `synchronous=NORMAL`, `busy_timeout` set, **one writer connection** + a pool of readers.
- Forward-only migrations, applied automatically on upgrade, **after** a `VACUUM INTO` snapshot of `state.db` (`snapshots/pre-migrate-*.db`, last 5 kept).
- Hourly `VACUUM INTO` snapshot (`snapshots/hourly-*.db`, last 24 kept), plus the latest snapshot is included in offsite backups.
- **Private files:** SQLite creates database files as 0644 regardless of the umask, and gives its `-wal`/`-shm` files the same mode. Wings creates `state.db` as 0600 before SQLite opens it, so all three stay root-only.

Tables (sketch): `servers`, `allocations`, `server_variables`, `eggs` (cached), `schedules`, `schedule_steps`, `jobs`, `job_logs`, `backups`, `backup_destinations`, `events` (outbox, with `seq`), `executed_commands`, `grant_cache`, `sftp_key_cache`, `metrics_rollup`, `kv` (node config).

## Container runtime

**Docker**, behind an internal `Runtime` interface so a different runtime could be swapped in later without touching the rest of Wings.

### Docker configuration (merged by installer)
```json
{
  "live-restore": true,
  "userland-proxy": false,
  "log-driver": "local",
  "log-opts": { "max-size": "20m", "max-file": "3" },
  "shutdown-timeout": 90
}
```
- `live-restore`: containers survive Docker daemon restarts. Applied by reload.
- `shutdown-timeout`: backstop for host shutdown. Servers are normally stopped gracefully first by `raptor-shutdown.service` (see [SERVERS.md](SERVERS.md#host-shutdown)).
- Containers use restart policy `no`; Wings starts servers itself after verifying the quota volume (see [SERVERS.md](SERVERS.md#after-a-reboot-or-wings-restart)).
- `userland-proxy: false`: kernel forwarding for published ports (important for UDP game traffic). **Requires a Docker restart.** If other containers are running, the installer asks before restarting. `doctor` reminds until it's applied.
- The Docker package is held (`apt-mark hold`) so unattended upgrades can't restart Docker at 3 AM. `raptor update` upgrades Docker deliberately.

### Networking
Wings sets up its runtime when it starts, and retries every minute until it succeeds (Docker may still be starting):
1. **Networks.** `raptor_nw` (bridge `raptor0`, containers can talk to each other) for servers and `raptor_install` (bridge `raptor-inst`, inter-container traffic off) for installs. Both are IPv4 bridges labeled `raptor.wings.managed=true`. Existing networks are reused as they are.
2. **Subnets** are picked when a network is created, unless the config sets one: the first `/16` from `172.29–31`, `172.24–28`, then `10.200–255` that doesn't overlap a host interface, a route in the kernel's routing table, or another Docker network. A configured subnet that overlaps one of those is an error, not a silent change.
3. **`raptor.slice`** memory ceiling (see [Resource limits](#resource-limits-performance-critical)).
4. **Firewall** table (see [Firewall](#firewall)).

- Servers get their allocated ports published on the node's IP(s), TCP and UDP, with the same port inside and outside the container (as in Pterodactyl).
- An allocation on **`127.0.0.1` is bound to the `raptor0` gateway address** instead (Pterodactyl does the same with its bridge). Host loopback isn't reachable from containers, while the gateway is reachable from the host and other servers (e.g. a proxy) but not from the internet. `SERVER_IP` stays `127.0.0.1`.
- Before creating a server's container, Wings checks every allocated port (TCP and UDP) is free on the host, so a port held by another program fails with a clear error.
- **Host networking mode** is an optional per-server setting for games that need it. No ports are published; the game binds them directly.
- Docker's iptables management stays on. Wings only ever publishes **allocated** ports. Preflight tells UFW users that game ports are managed by Raptor, not UFW.

### Firewall
Wings' rules live in their **own nftables table, `inet raptor`**, not in Docker's chains. Its base chains hook `forward`, `input`, and `output` at priority `filter - 1`, so they run before Docker's. nftables evaluates every table on a hook and a drop is final, so these rules can't be bypassed by Docker's accepts, and Docker rewriting its own rules never removes them. The table is replaced atomically (one `nft -f` transaction) and checked every minute: if something removed it (e.g. `nft flush ruleset`, which Debian's `nftables.service` runs on restart), Wings reapplies it and logs a warning.

| Rule | Applies to |
|---|---|
| Drop the cloud metadata endpoint (`169.254.169.254`, `fd00:ec2::254`) | All Raptor containers: forwarded traffic from both bridges, and host-networked servers via their cgroup (`raptor.slice`) |
| Drop private, loopback, link-local, CGNAT, multicast, and reserved ranges (IPv4 and IPv6) | Install containers (`raptor-inst`) |
| Drop everything addressed to the host itself | Install containers |
| Allow port 53 to the host's upstream DNS resolvers, even in private ranges | Install containers |
| Allow `docker.install_allow` CIDRs | Install containers |

- **DNS for installs:** Docker's embedded resolver forwards container queries to the host's resolvers from inside the container's network namespace, so those packets are filtered like any other. Home boxes usually resolve through the router (`192.168.x.1`), so without the DNS exception every install would fail. Wings reads the resolvers from `/etc/resolv.conf`, or from systemd-resolved's upstream list when it points at the local stub.
- Game servers can still reach private ranges, the host, and each other, because proxies like Velocity talk to backend servers over them.
- Future per-server rules (IP allowlists, blocks) go in the same table.

### Resource limits (performance-critical)
- **Memory:** container limit = server memory + overhead (Pterodactyl-compatible overhead rules), so Java heap = allocated memory doesn't get the container OOM-killed. Swap configurable per server (default: none).
- **CPU:** default is **CPU weight (shares)**, not a hard CFS quota. Hard CFS quotas cause throttling stalls that show up as tick lag in Minecraft and similar games. A hard limit is available per server. Optional **CPU pinning** (cpuset) for dedicated cores.
- **PIDs:** limit per container (egg `pid_limit` feature respected).
- **I/O:** game servers get normal I/O weight. Backup and install jobs run with low I/O weight so they can't cause lag. Docker sets the weight in `io.weight`, but the kernel only enforces it with the BFQ scheduler or the `io.cost` controller; with `none` or `mq-deadline` (common defaults) it has no effect.
- **`raptor.slice`:** every Raptor container runs in this systemd slice (Docker's `cgroup-parent`). Its `MemoryMax` is total RAM minus a reserve for the OS, Docker, and Wings (10% of RAM, at least 1 GiB and at most 4 GiB, or `limits.reserved_memory`), so servers together can never starve the host. Wings sets it at runtime on every start, so it follows RAM changes. Needs Docker's systemd cgroup driver (the default on Debian 12); with `cgroupfs` Wings logs a warning and runs without the ceiling.
- Wings itself runs with `OOMScoreAdjust=-900`.

### Container hardening
- Non-root user inside the container (Pterodactyl-compatible UID)
- Drops the same capabilities as Pterodactyl (`SETPCAP`, `MKNOD`, `AUDIT_WRITE`, `NET_RAW`, `DAC_OVERRIDE`, `FOWNER`, `FSETID`, `NET_BIND_SERVICE`, `SYS_CHROOT`, `SETFCAP`); `no-new-privileges`
- Docker's default seccomp profile
- Read-only root filesystem; `/home/container` and a 100 MB `/tmp` tmpfs are the only writable paths
- Only the server's own directory is mounted. Nothing else from the host, ever.
- Tested for real by `task e2e:runtime` (see [CONTRIBUTING.md](../CONTRIBUTING.md#runtime-end-to-end-tests)).

## Disk quotas

**XFS project quotas.** Kernel-enforced, zero overhead, instant usage readout (no `du` scans), instant limit changes.

| Tier | When | How |
|---|---|---|
| 1 | `/var/lib/raptor/volumes` is on XFS with `prjquota` (or `--data-disk` given) | Use it directly |
| 2 | Anything else (default ext4 Debian/Ubuntu) | One **preallocated** file (`fallocate`, size chosen at install, default ~80% of free space), formatted XFS, loop-mounted with **direct I/O** at `/var/lib/raptor/volumes` |
| 3 | Owner passes `--no-quota` | Soft limits: periodic usage scan, warn then stop server when over |

- Quotas set via syscalls (`quotactl`, `FS_IOC_FSSETXATTR`), not shell commands.
- The loop image is mounted by a **systemd mount unit**, ordered after local filesystems and **before** Docker and Wings.
- **Wings refuses to start any server if the volume isn't mounted.** This prevents writing into the empty mount point underneath.
- `raptor storage grow <size>` grows the image and runs `xfs_growfs` online.
- `doctor` checks mount state, free space, and can run `xfs_repair` (with servers stopped).
- Docker images stay on the host disk (`/var/lib/docker`); only server data lives on the quota volume.

## Job engine

A durable queue in SQLite. Everything long-running is a job: `server.install`, `server.reinstall`, `server.update`, `backup.create`, `backup.restore`, `backup.prune`, `schedule.run`, `logs.rotate`, `transfer.send`, `transfer.receive`.

- Jobs survive Wings restarts and reboots, and resume or retry with backoff.
- **Per-server lock:** one mutating job per server at a time.
- **Global limits:** e.g. max 2 concurrent backups, 2 concurrent installs per node.
- Every job has persisted logs (`raptor jobs logs <id>`, and the Panel).
- Every job emits events to the outbox.

## Scheduler

- Cron expressions with a **per-schedule timezone** (DST handled by the cron library).
- **Multi-step** tasks: `command`, `wait`, `power`, `backup`.
- `only_when_online` flag, optional jitter.
- Missed runs: `skip` (default) or `run_once_on_boot`. Never replays a backlog.

## Backups

- Engine: **Kopia** (Go library, incremental, deduplicated, compressed, encrypted).
- **Game-aware hooks** from the egg's `x-raptor` extension (e.g. Minecraft `save-off` / `save-all` before, `save-on` after).
- **On by default** for new servers: daily, local destination, keep 7.
- Destinations: local, S3-compatible (S3, B2, R2, Wasabi…), Raptor hosted storage.
- **Encryption key:** per-node key, stored encrypted in the Panel **by default** (so backups survive a dead box). Optional owner-held key mode with an explicit "lose the key, lose the backups" warning.
- Retention policies (daily/weekly/monthly), pruning as a separate job.
- **Restore takes a safety backup first.**
- Runs with low I/O and CPU weight.

## Crash detection and restart

Exact rules (what counts as a crash, backoff, crash-loop thresholds) are in [SERVERS.md](SERVERS.md#crash-policy).

- Watches container exits and egg "done"/health signals.
- Restart with backoff. After N crashes in M minutes, stop and mark `crashed` (no infinite restart loops).
- On by default.

## Notifications

Sent **directly from Wings** so they work during Panel outages: Discord webhooks, generic webhooks. Email goes through the Panel.

## Local metrics

~7 days of CPU/RAM/disk/network/player history per server in SQLite, downsampled over time. Streamed live to the Panel only while someone is watching.

## Files and SFTP

**Wings runs no HTTP server.** It only listens on the game servers' ports and, when enabled, SFTP.
- **Web file manager:** operations arrive over the node connection. Uploads and downloads are chunked (under 100 MB per chunk, resumable, 1 GB per file) and carried on a separate short-lived outbound connection per transfer, so they never slow the console. See [ARCHITECTURE.md](ARCHITECTURE.md#files-and-sftp).
- **SFTP** (off by default, enabled per node in the Panel): built-in server (`golang.org/x/crypto/ssh`), usernames `user.serverid`, chrooted to the server's directory, reachable at `n-<short-id>.raptornodes.net`. For files over the web cap and bulk transfers.
- SFTP auth via the Panel. Cached SSH public keys + permissions allow key auth while the Panel is unreachable.
- SFTP host key generated on the node; its fingerprint is reported to the Panel and shown to users. **Nodes need no TLS certificates.**
- **All file operations use `os.Root`** (symlink-safe, confined to the server directory).

## CLI scope

The CLI keeps things running. **It never changes server configuration.** All setup happens in the Panel.

```
raptor status                         node health, Panel link, disk, Docker
raptor doctor [--bundle [--upload]]   diagnose + fix suggestions; offline support bundle
raptor update                         update Wings (and Docker, deliberately)
raptor link --token … | unlink | relink
raptor ps                             servers + state + CPU/RAM
raptor start|stop|restart|kill <server>
raptor console <server>               live console
raptor logs <server> [-f]
raptor backup list|create|restore <server> [id]
raptor jobs [logs <id>]
raptor storage status|grow <size>
raptor support status|revoke          see / end active support access
raptor import pterodactyl             migrate servers from Pterodactyl Wings
raptor uninstall [--wipe-data]
raptor tui
```

### Local socket API

A **small dedicated service**, `raptor.wings.local.v1.LocalService` (in `proto/`), served on `/run/raptor/wings.sock` over Connect (HTTP/1.1 or unencrypted HTTP/2 on the Unix socket). It's not the Panel API, and it has no methods that change server configuration.

Methods are added to the proto as the features behind them are built, so the API never exposes placeholders. Implemented so far: `GetStatus` (`raptor status`) and `ShutdownServers` (root only; `raptor wings shutdown-servers`, called by `raptor-shutdown.service`). The full planned set:

| Method | Purpose |
|---|---|
| `GetStatus` | Node health, Panel link, Docker, disk, version |
| `ShutdownServers` | Graceful stop of every server for a host shutdown, keeping `desired_state` (root only) |
| `ListServers`, `GetServer` | Servers, states, resource usage |
| `Start`, `Stop`, `Restart`, `Kill` | Power actions |
| `AttachConsole` (stream) | Console output + sending commands |
| `TailLogs` (stream) | Server logs |
| `ListBackups`, `CreateBackup`, `RestoreBackup` | Backups |
| `ListJobs`, `TailJobLogs` (stream) | Jobs and their logs |
| `RunDoctor`, `CreateBundle` | Diagnostics |
| `GetStorage`, `GrowStorage` | Quota volume |
| `GetSupportStatus`, `RevokeSupport` | Support access |
| `Link`, `Unlink`, `Relink` | Panel linking |
| `Update` | Self-update |

- **Access:** Unix socket permissions only: the socket is `0660 root:raptor` (root-only `0600` if the `raptor` group doesn't exist). No passwords or tokens.
- **Attribution:** Wings reads the caller's Unix user from the socket (`SO_PEERCRED`). Every mutating call is recorded as an event with actor `local:<username>`, so it appears in the Panel's audit log.

## `doctor`

Checks, each with **what's wrong, why it matters, and how to fix it**:
- OS/kernel/arch supported, cgroups v2, systemd
- Docker running, configured (`live-restore`, `userland-proxy`), version
- Quota volume mounted, healthy, free space
- Host disk free space (Docker images, SQLite)
- Clock synced (NTP). Clock drift breaks connection signatures, grants, and schedules.
- Panel reachable through Cloudflare (`wss://raptorpanel.net`), node key present
- SFTP port bound (when enabled); node hostname resolves to this box's public IP
- Pterodactyl coexistence
- Security warnings (warn only, never change): password root SSH login, unattended upgrades off

`--bundle` writes a redacted `.tar.gz` (doctor output, Wings logs, Docker info, system info, install log; **no server files, no secrets**). `--upload` sends it over HTTPS and prints a code like `RPT-7K2M`.

## Updates

- Channels: `stable`, `beta`. Optional version pin.
- Staged rollout controlled by the Panel (e.g. 5% → 25% → 100%).
- Download `checksums.txt` + `checksums.txt.minisig` → **verify the signature** with the embedded public key → download the binary → **verify its SHA-256** → atomic binary swap → `systemctl restart raptor-wings` (servers unaffected) → health check.
- **Automatic rollback** to the previous binary if the new version fails to start or can't reconnect within 5 minutes.
- SQLite migrations never break the previous minor version's ability to read state (so rollback is safe), or they block rollback explicitly.

## Pterodactyl import

`raptor import pterodactyl`:
1. Reads `/etc/pterodactyl/config.yml` and the Pterodactyl Panel's server list (via the owner's Pterodactyl API key).
2. For each server: stop in Pterodactyl → move files (same filesystem) or copy into Raptor's volume → create in Raptor with the same egg, variables, and allocation → start → verify.
3. After the last server, offer to disable `wings.service`. **Never deletes** Pterodactyl's config or data.
