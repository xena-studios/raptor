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
| Service | `raptor-wings.service` |
| Config | `/etc/raptor/config.yml` |
| State DB | `/var/lib/raptor/state.db` |
| Server data | `/var/lib/raptor/volumes/<server-id>/` (quota volume) |
| Logs | `/var/log/raptor/` |
| Socket | `/run/raptor/wings.sock` (root + `raptor` group) |
| System user | `raptor` |
| Docker network | `raptor_nw`, bridge `raptor0`, subnet chosen at install to avoid collisions |
| Container labels | `raptor.wings.managed=true`, `raptor.wings.server=<id>`, `raptor.wings.node=<id>`, `raptor.wings.role=server\|install` |
| Firewall | `RAPTOR` chain, one jump rule from `DOCKER-USER` |

### Coexistence with Pterodactyl's Wings
- Different binary, service, paths, network, subnet, labels, and user. No shared resources.
- Wings **only lists and manages containers labeled `raptor.wings.managed=true`**. It never prunes or touches others.
- Before accepting a port allocation, Wings checks that the host port is free. The Panel rejects allocations Wings reports as taken.
- SFTP uses 2022 unless it's taken (Pterodactyl's default), then the first free port in 2022–2099. HTTPS files use 8443, falling back within 8443–8499. The Panel always shows the real port.
- The installer **merges** into `/etc/docker/daemon.json`, never overwrites it.
- A CI test runs both Wings on one VM and checks neither disturbs the other.

## systemd service

`install/systemd/raptor-wings.service` (installed by `raptor bootstrap`; in development by `task wings:vm:install`):
- `Restart=always`, `OOMScoreAdjust=-900`, `LimitNOFILE=65536`.
- **Stopping or restarting the service never stops servers:** their containers live in Docker's cgroups, not the service's.
- `UMask=0077`, and systemd creates `/var/lib/raptor`, `/var/log/raptor`, and `/etc/raptor` as `0700`. `/run/raptor` is `0755` so the `raptor` group can reach the socket.
- Sandboxing: `ProtectSystem=strict` (writable: `/etc/raptor`, `/var/lib/raptor`, `/var/log/raptor`, `/run/raptor`), `ProtectHome`, `PrivateTmp`, `NoNewPrivileges`, kernel module/log/clock/hostname/cgroup protection, `RestrictNamespaces`, `RestrictSUIDSGID`, native syscalls only, and only `AF_UNIX`/`AF_INET`/`AF_INET6`/`AF_NETLINK` sockets. `systemd-analyze security` rates it 5.9 (medium). Wings needs root for Docker, quotas, and nftables, so it can't go much lower; this will be revisited as those features land.
- On shutdown Wings stops the local API (the socket is removed) and closes the database.

## Config file

`/etc/raptor/config.yml` holds **only static, box-level settings**, written by the installer and rarely changed. Everything about servers, schedules, and backups lives in SQLite and comes from the Panel. Owned by root, mode `0600`.

```yaml
node_id: 0192f0a4-...            # assigned at enrollment
panel:
  url: https://raptorpanel.net   # never hardcoded in Wings
  tunnel: tunnel.raptorpanel.net:443
tls:                             # all files root-only, 0600
  ca: /etc/raptor/tls/panel-ca.pem
  cert: /etc/raptor/tls/node.pem
  key: /etc/raptor/tls/node.key  # generated on the box, never leaves it
paths:
  state: /var/lib/raptor/state.db
  volumes: /var/lib/raptor/volumes
  tmp: /var/lib/raptor/tmp
  socket: /run/raptor/wings.sock
docker:
  network: raptor_nw
  subnet: 172.29.0.0/16          # chosen at install to avoid collisions
  install_network: raptor_install
  install_subnet: 172.30.0.0/16
ports:
  sftp: 2022
  https: 8443
limits:
  concurrent_installs: 2
  concurrent_backups: 2
  host_disk_min_free: 10GiB      # below this: refuse installs and image pulls
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
- Servers get their allocated ports published on the node's IP(s).
- **Host networking mode** is an optional per-server setting for games that need it.
- Install containers use a separate `raptor_install` network: outbound internet only, with the host, private ranges, `raptor_nw`, and link-local addresses blocked (see [EGGS.md](EGGS.md#install)).
- All Raptor containers are blocked from the cloud metadata endpoint (`169.254.169.254`). Game servers can still reach private ranges, because proxies like Velocity talk to backend servers over them.
- Docker's iptables management stays on. Wings only ever publishes **allocated** ports, and its own rules (IP allowlists, blocks) live in the `RAPTOR` chain. Preflight tells UFW users that game ports are managed by Raptor, not UFW.

### Resource limits (performance-critical)
- **Memory:** container limit = server memory + overhead (Pterodactyl-compatible overhead rules), so Java heap = allocated memory doesn't get the container OOM-killed. Swap configurable per server (default: none).
- **CPU:** default is **CPU weight (shares)**, not a hard CFS quota. Hard CFS quotas cause throttling stalls that show up as tick lag in Minecraft and similar games. A hard limit is available per server. Optional **CPU pinning** (cpuset) for dedicated cores.
- **PIDs:** limit per container (egg `pid_limit` feature respected).
- **I/O:** game servers get normal I/O weight. Backup and install jobs run with low I/O weight so they can't cause lag.
- Game server containers run in a `raptor.slice` cgroup with a total memory ceiling below the box's RAM, leaving room for the OS, Docker, and Wings.
- Wings itself runs with `OOMScoreAdjust=-900`.

### Container hardening
- Non-root user inside the container (Pterodactyl-compatible UID)
- Drop all capabilities except what eggs require; `no-new-privileges`
- Default seccomp profile
- Read-only root filesystem where the image allows it; `/home/container` is the only writable mount
- Only the server's own directory is mounted. Nothing else from the host, ever.

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

## SFTP and HTTPS file server

- Built-in SFTP server (`golang.org/x/crypto/ssh`), usernames `user.serverid`, chrooted to the server's directory.
- Auth via the Panel. Cached SSH public keys + permissions allow key auth while the Panel is unreachable.
- Host key signed by the Panel's CA at enrollment. The Panel shows the fingerprint.
- HTTPS file endpoint on 8443 for large uploads/downloads with short-lived signed tokens; cert for `<node-id>.node.raptornodes.net` delivered by the Panel.
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

Methods are added to the proto as the features behind them are built, so the API never exposes placeholders. Implemented so far: `GetStatus` (`raptor status`). The full planned set:

| Method | Purpose |
|---|---|
| `GetStatus` | Node health, Panel link, Docker, disk, version |
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
- Clock synced (NTP). Clock drift breaks certs, tokens, and schedules.
- Tunnel reachable, cert validity
- SFTP/HTTPS ports bound
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
