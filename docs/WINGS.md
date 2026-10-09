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

One static Go binary, `/usr/local/bin/raptor` (no CGO; SQLite via `modernc.org/sqlite`). It's a symlink to the active version in `/usr/local/lib/raptor` (see [Updates](#updates)).

| Command | What |
|---|---|
| `raptor wings run` | The daemon (started by `raptor-wings.service`) |
| `raptor <cmd>` | CLI, talks to the daemon over `/run/raptor/wings.sock` |
| `raptor tui` | Terminal UI |
| `raptor bootstrap` | Install/enroll (called by the install script) |

## On-box footprint

| Resource | Value |
|---|---|
| Binary | `/usr/local/bin/raptor` → `/usr/local/lib/raptor/current` → `raptor-<version>` (the active version, plus the previous one for rollback) |
| Update state | `/var/lib/raptor/update.json` (the last update and its outcome) |
| Services | `raptor-wings.service`; `raptor-shutdown.service` (graceful stops on host shutdown) |
| systemd drop-in | `/etc/systemd/system/docker-.scope.d/10-raptor.conf` (`Before=raptor-shutdown.service`): container scopes stop only after `raptor-shutdown` has stopped servers gracefully ([SERVERS.md](SERVERS.md#host-shutdown)) |
| Config | `/etc/raptor/config.yml` |
| State DB | `/var/lib/raptor/state.db` |
| Server data | `/var/lib/raptor/volumes/<server-id>/` (quota volume) |
| Logs | `/var/log/raptor/` (job logs, including installs, in `jobs/<job-id>.log`) |
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
- Sandboxing: `ProtectSystem=strict` (writable: `/etc/raptor`, `/var/lib/raptor`, `/var/log/raptor`, `/run/raptor`, and `/usr/local/lib/raptor` for updates), `ProtectHome`, `PrivateTmp`, `NoNewPrivileges`, kernel module/log/clock/hostname/cgroup protection, `RestrictNamespaces`, `RestrictSUIDSGID`, native syscalls only, and only `AF_UNIX`/`AF_INET`/`AF_INET6`/`AF_NETLINK` sockets. `systemd-analyze security` rates it 5.9 (medium). Wings runs `nft` and `systemctl` itself (firewall and slice setup) and both work inside this sandbox. Wings needs root for Docker, quotas, and nftables, so it can't go much lower; this will be revisited as those features land.
- On shutdown Wings stops the local API (the socket is removed), detaches from servers (they keep running), and closes the database.
- When the runtime is ready, Wings starts the server manager, which reconciles with Docker: it reattaches to running servers and starts those that should run ([SERVERS.md](SERVERS.md#after-a-reboot-or-wings-restart)). Server containers run as the `raptor` user's UID/GID, with the host's time zone as `TZ`.

## Config file

`/etc/raptor/config.yml` holds **only static, box-level settings**, written by the installer and rarely changed. Everything about servers, schedules, and backups lives in SQLite and comes from the Panel. Owned by root, mode `0600`.

```yaml
node_id: 0192f0a4-...            # assigned at enrollment
panel:
  url: https://api.raptorpanel.net  # never hardcoded in Wings
  app_url: https://app.raptorpanel.net  # passkey origin and RP ID for signed commands (https; http only for localhost, a dev web app)
identity:                        # written at enrollment, root-only 0600
  key: /etc/raptor/node.key      # the node's private key; generated on the box, never leaves it
  panel_key: /etc/raptor/panel.pub  # the Panel's signing key, pinned at enrollment
  sftp_host_key: /etc/raptor/sftp_host_key  # Ed25519, generated on first start, never leaves the box
paths:
  state: /var/lib/raptor/state.db
  volumes: /var/lib/raptor/volumes
  backups: /var/lib/raptor/backups   # the local backup destination
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
  backup_memory: 1GiB            # memory cap of each backup worker process
storage:
  quotas: true                   # false = soft disk limits by scanning (tier 3)
updates:
  channel: stable
  pin: ""                        # e.g. "1.4.2": install this version and stay on it
  automatic: true                # the Panel's staged rollouts may update this node
log:
  level: info
notifications: []                # see Notifications below
```

- Unknown keys are an error, so typos don't go unnoticed.
- `raptor doctor` validates the file.
- Changes need `systemctl restart raptor-wings` (servers are unaffected).

## Local state (SQLite)

- **WAL mode**, `synchronous=NORMAL`, `busy_timeout` set, **one writer connection** + a pool of readers.
- Forward-only migrations, applied automatically on upgrade, **after** a `VACUUM INTO` snapshot of `state.db` (`snapshots/pre-migrate-*.db`, last 5 kept).
- Hourly `VACUUM INTO` snapshot (`snapshots/hourly-*.db`, last 24 kept) and one on every clean stop (`snapshots/shutdown-*.db`, last 3), plus the latest snapshot is included in offsite backups. A corrupt database is replaced by the newest good snapshot when Wings starts ([RELIABILITY.md](RELIABILITY.md#state-durability)).
- **Private files:** SQLite creates database files as 0644 regardless of the umask, and gives its `-wal`/`-shm` files the same mode. Wings creates `state.db` as 0600 before SQLite opens it, so all three stay root-only.

Tables: `servers` and `allocations` (Phase 1.4); `jobs`, `events` (outbox, with `seq`), `executed_commands`, and `trusted_keys` (Phase 1.5); `schedules` (Phase 2, steps stored in it as JSON); `backups`, `backup_destinations`, and `backup_policies` (Phase 2); `kv` (node config, the backup repository password, and whether SFTP is on and its port). Still to come with their features: `grant_cache`, `metrics_rollup`.

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
- Docker's default seccomp profile, minus one call: `ioctl` is allowed for every command except `FS_IOC_FSSETXATTR`. A file's owner can otherwise move it to another quota project without any capability, escaping its disk limit ([Disk quotas](#disk-quotas)). The rule is written as allow-only because in libseccomp a matching allow beats a matching deny; values with extra high bits don't match any allow (the kernel truncates the command to 32 bits), and sign-extended values from musl are allowed. Install containers get the same profile.
- Read-only root filesystem; `/home/container` and a 100 MB `/tmp` tmpfs are the only writable paths
- Only the server's own directory is mounted. Nothing else from the host, ever.
- Tested for real by `task e2e:runtime` (see [CONTRIBUTING.md](../CONTRIBUTING.md#runtime-end-to-end-tests)).

## Disk quotas

**XFS project quotas** (`internal/wings/storage`). Kernel-enforced, no overhead on normal I/O, instant usage readout (no `du` scans), and limit changes that apply instantly, even while the server runs.

| Tier | When | How |
|---|---|---|
| 1 | `/var/lib/raptor/volumes` is on XFS mounted with `prjquota` (e.g. a data disk) | Use it directly. Full native speed. |
| 2 | Anything else (the default ext4 Debian/Ubuntu box) | **Default.** `raptor storage setup -size <size>` preallocates one image file (`/var/lib/raptor/volumes.xfs`), formats it XFS, and loop-mounts it with **direct I/O** at `/var/lib/raptor/volumes` |
| 3 | Owner opts out (`storage.quotas: false`) | Soft limits: usage is scanned every 5 minutes; over the limit, the console gets a warning, then the server is stopped and can't start until it's under |

**Tier 2 costs fsync'd writes about half their speed**, measured on a real Linux runner (ext4 host disk vs the loop volume, fio, 5 runs each, median):

| Test | Loop volume vs native |
|---|---|
| Sequential write / read (1 MiB) | 100% / 99% |
| Random write / read (4 KiB) | 184–206% / 100% |
| Buffered writes | 90–100% |
| **16 KiB writes with fsync each (game saves)** | **48–56%** |

Each fsync commits two journals: the XFS journal inside the image, and then the host filesystem's journal for the image file. Turning the loop device's direct I/O off made it worse (41%). In absolute terms the loop volume still sustained about 1,000–2,900 fsync'd saves per second on a basic cloud disk, which is far more than game servers do. Hard limits stay the default, because a limit that can be overrun between scans doesn't protect the other servers on the box. The installer explains the trade-off, and boxes that want native speed use a data disk (tier 1).

How it works:
- Each server gets a **quota project** ID (from 1000, stored with the server). Wings sets it on the server's directory with `FS_IOC_FSSETXATTR` and the inherit flag, so everything created inside belongs to it, and sets and reads limits with `quotactl_fd`. No shell commands.
- Before every install and start, Wings puts the directory (and anything already in it) in the server's project and applies its `disk_mib` limit (`0` = unlimited). Deleting a server clears its limit.
- A full quota shows up in the game as **"No space left on device"** (XFS reports project quotas as `ENOSPC`, not `EDQUOT`).
- **Quota escape, blocked:** a file's owner (or root in a container) can move a file into another project with `FS_IOC_FSSETXATTR`, no capability needed, and so write past its limit. The gate reproduced it from a server container. Raptor's seccomp profile refuses that one `ioctl` in every container ([Container hardening](#container-hardening)).
- The image is mounted by a **systemd mount unit** that Wings writes (`var-lib-raptor-volumes.mount`), with `DefaultDependencies=no`: after local filesystems (and whichever holds the image), before Docker and Wings, and unmounted only after they stop. With the default dependencies, the unit is ordered before `local-fs.target` and after local filesystems at once, a cycle that systemd resolves by silently not mounting it at boot (caught by the gate). It's wanted by `multi-user.target`, not `local-fs.target`, so a broken volume never drops a remote box into emergency mode.
- A mount unit can't turn on direct I/O, so Wings does it on every start.
- **Wings refuses to install or start any server if the volume isn't mounted with project quotas.** This prevents writing into the empty mount point underneath, onto the host disk without limits. Wings itself keeps running and says why.
- `raptor storage grow -size <size>` extends the image, refreshes the loop device, and runs `xfs_growfs`, all online. Setup and grow keep `limits.host_disk_min_free` free on the host.
- `doctor` checks mount state and free space. (Offering `xfs_repair` with servers stopped is still to come.)
- Docker images stay on the host disk (`/var/lib/docker`); only server data lives on the quota volume.
- Tested by `task e2e:quotas` (see [CONTRIBUTING.md](../CONTRIBUTING.md#runtime-end-to-end-tests)).

## Job engine

A durable queue in SQLite (`internal/wings/jobs`). Everything long-running is a job. Implemented: `server.install` (installs and reinstalls), `server.delete`, `schedule.run`, `backup.create`, `backup.restore`, `backup.delete`, `backup.maintain`, `files.compress`, and `files.decompress`. Coming with their features: `transfer.send`, `transfer.receive`.

- **Survives Wings restarts and reboots.** A job that was running when Wings stopped is **resumed** if its handler says re-running it is safe (installs are: the script runs over the existing files again), and otherwise marked failed with "interrupted". Interruptions count as attempts, so a job that crashes Wings can't loop forever.
- **Checkpoints:** a job can save its progress while it runs; a resumed job gets it back and continues from there instead of starting over (schedule runs continue after their last finished step; a resumed backup sends the egg's post-backup commands first; a resumed restore doesn't take a second safety backup).
- **Created atomically with what it's for:** a server, its "created" event, and its install job are written in one SQLite transaction.
- **Retries:** a handler can mark an error as retryable; the job is requeued with exponential backoff (30 s doubling, at most 30 min) until its attempts run out.
- **Per-server lock:** at most one locked job per server runs at a time.
- **Global limits** per class: installs `limits.concurrent_installs` (default 2), backups `limits.concurrent_backups` (default 2), schedule runs 32 (they mostly wait), everything else 4.
- **Cancel** queued or running jobs; deleting a server cancels its jobs first.
- **Logs:** each job's output is kept in `/var/log/raptor/jobs/<job-id>.log`, the last 10 MB of it, written every 5 seconds while it runs (so a crash loses little) and at the end. Readable live while the job runs.
- A handler panic fails the job; it never takes Wings down.
- **Status events:** every change to a server's job's status (queued, running, requeued for a retry, succeeded, failed, cancelled) is a `job.status` event, written in the same transaction, so the Panel's mirror follows it; a server's 50 most recent jobs come with it in `GetServers`. Logs stay on the node.
- Finished jobs and their logs are deleted after 30 days.

## Event outbox

Every change on the node is appended to `events` with a **monotonic sequence number** (`internal/wings/events`); it's what the Panel's mirror is built from ([ARCHITECTURE.md](ARCHITECTURE.md#mirror-sync)).
- Events describing a database change are written **in the same transaction** as the change, so an event exists if and only if its change happened.
- Readers ask for everything after the last sequence number they have; a sequence number is never reused, even after pruning.
- **Retention:** acknowledged events are kept 7 days; unacknowledged ones up to the newest 100,000, so a node that never reaches a Panel can't grow its database forever. A reader that fell behind what's kept gets `ErrGap` and must rebuild from a snapshot.

## Commands from the Panel

`internal/wings/command` receives commands (over the [node connection](#node-connection)) and decides whether to run them. See [SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands).
- **Envelope:** `command_id` (UUIDv7), node, user, action, server, params, and an expiry at most 10 minutes out.
- **Panel grant:** an Ed25519 signature by the Panel's pinned key over the user, node, command ID, action, and server. Bound to one command, so it can't be reused. No Panel key (not linked yet) means every command is refused.
- **Passkey signature** for dangerous actions, verified by Wings itself.
- **Exactly once:** each `command_id` runs at most once. A retry of the same command returns the stored result (even after it expired); the same ID with different content is refused. Records are kept 7 days. Commands left running by a Wings crash are marked failed on start.
- **Actions** (`internal/wings/actions`): `server.create` (signed), `server.update` (without an egg it keeps the server's own, which is how the Panel changes settings, since it doesn't hold egg files; signed when it changes the egg, image, or startup command, decided by Wings from its own records), `server.delete` (signed), `server.reinstall`, `server.start`/`stop`/`restart`/`kill`, `server.command`, `schedule.create`/`update`/`delete`/`run` (unsigned: a schedule can only do what the user could already do unsigned), `backup.create`, `backup.restore` (signed), `backup.delete` (signed), `backup.lock` (signed to unlock), `backup.policy.update` (signed when it lowers any keep value), `backup.destination.save`/`delete`, `keys.add`/`keys.remove` (signed by an owner key), and `node.update` (unsigned, see [Updates](#updates)).

## Node connection

`internal/wings/link` keeps the node connected to the Panel ([ARCHITECTURE.md](ARCHITECTURE.md#node-connection)); the wire protocol, shared with the Panel, is `internal/shared/nodelink`.
- **Only a linked node connects:** `node_id` set, the node key (`identity.key`: the base64 Ed25519 seed, generated on the box, mode `0600`; Wings won't use one other users can read) and the pinned Panel key present. If one is missing, Wings logs why and runs without the Panel.
- **Always reconnecting, never required:** full-jitter backoff from 1 s to 60 s; servers, schedules, and backups don't notice the connection at all.
- **What the Panel can call:** `Execute` (a command, through the executor above) and `Events` (the [outbox](#event-outbox), which it acknowledges by asking for what comes after). Wings calls `EventsAvailable` on the Panel whenever there are new events.
- `raptor status` shows the connection: connected for how long, the last ping's round trip, and reconnects; or why it isn't connected.

## Scheduler

`internal/wings/schedule`, with cron parsing in `internal/cron` (shared with the Panel, which uses it to validate schedules and show the next run). Schedules live in SQLite and fire from Wings, whether or not the Panel is reachable.

- **When:** a standard five-field cron expression (minute, hour, day of month, month, day of week; names like `mon` and `jan`; the macros `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`) in the **schedule's own IANA time zone** (default UTC). Time zone data is built into Wings. An expression that can never run (`0 0 30 2 *`) is refused.
- **Daylight saving time,** defined by Raptor rather than left to a library:
  - A run whose time is skipped when the clocks go forward (02:30 on the night 02:00 becomes 03:00) runs once, when the clocks change.
  - A time that happens twice when the clocks go back runs once, the first time. A schedule that runs every hour keeps its rhythm through the repeated hour too.
  - Checked against a brute-force minute-by-minute reference in zones with 30-minute and midnight clock changes, and fuzzed.
- **Steps** (1–20), run in order: `command` (a console command), `wait` (1 s to 1 h), `power` (`start`, `stop`, `restart`, `kill`), `backup` (back up the server and wait for it; a resumed run waits for the same backup). A failed step ends the run unless it has `continue_on_failure`. Runs are attributed to `schedule:<id>` in the audit events.
- **`only_when_online`:** scheduled runs are skipped while the server isn't running. "Run now" always runs.
- **Jitter** (up to 1 h): every run is delayed by the same amount, derived from the schedule's ID, so schedules set for the same minute don't all start together, and the next run shown is the real one.
- **One run at a time:** if the previous run is still going, the new one is skipped (with an event saying so).
- **Late and missed runs:** a run up to 5 minutes late (a Wings restart) still runs. Later than that it was missed, and the schedule's policy decides: `skip` (default) or `run_once` (run once now). Missed runs are never replayed one by one; the next run is the next one on the clock.
- **Runs are jobs** (`schedule.run`). The steps are copied into the job when it fires, so editing a schedule doesn't change a run in progress. Firing a run and moving the schedule to its next time happen in one transaction, so a crash can't fire it twice.
- **Wings restarts during a run:** the run continues after its last finished step (a wait continues until its original end). A power step that was in progress isn't done again if it already took effect: a start or restart whose container started after the step began (Docker's record, which survives the restart), or a stop or kill that left the server stopped. Otherwise Wings stopping in the moment between a restart finishing and the run saving its progress would restart the game twice. A step that can't be confirmed is done again. If Wings was down for more than 5 minutes, the rest of the run is skipped instead: restarting a server long after its players were warned would be worse than not restarting it. Wings knows how long it was down from a heartbeat the scheduler writes about once a minute.
- **Events:** `schedule.created`/`updated`/`deleted`, `schedule.run.queued`, `schedule.run.skipped` (with the reason: `missed`, `offline`, `still_running`), and `schedule.run.finished` (each step's result); they carry the next run time for the Panel's mirror. Deleting a server deletes its schedules; deleting a schedule cancels its run in progress.
- Limits: 50 schedules per server.

## Backups

`internal/wings/backup`, with the Kopia work in `internal/wings/backup/engine`.

- **Engine: Kopia**, used as a Go library: incremental, deduplicated, compressed (zstd), encrypted.
- **One repository per destination**, shared by every server on the node, so a file several servers have (a server jar, a plugin) is stored once. Each server's backups are a separate snapshot source, so each backup is incremental against that server's last one.
- **Destinations:** `local` (always there: `paths.backups`, on the host disk) and S3-compatible buckets (S3, B2, R2, Wasabi, MinIO…), added by the owner. S3 credentials are stored in SQLite and never appear in events or listings. Deleting a destination forgets its backups; the data in the bucket is left alone. Raptor hosted storage comes with the Panel (Phase 5).
- **Encryption key:** one random repository password per node, generated on first use and kept in SQLite. When the node is linked (Phase 3), a copy is stored encrypted in the Panel **by default** (so backups survive a dead box). Optional owner-held key mode, with an explicit "lose the key, lose the backups" warning.
- **On by default:** a new server gets a "Daily backup" schedule (04:00 to 05:00 in the node's time zone, spread by jitter, made up once if the node was off, skipped while the server is offline) with a `backup` step, unless the create command asks for none. It's an ordinary schedule: the owner can move, disable, or delete it.
- **Per-server settings:** the destination, retention, and ignore patterns (gitignore style; a `.pteroignore` file in the server directory also works, for servers moved from Pterodactyl). Defaults: local, keep the last 3, 7 daily, 4 weekly.
- **Retention** runs after every backup of the server. Each rule keeps the newest backup in each of its last N periods that have a backup (days, ISO weeks, and months in the node's time zone), so a server that was offline for a month doesn't lose its backups to the calendar. Anything a rule keeps is kept. **Locked** backups are never deleted by retention; at least one keep value must be above zero.
- **Game-aware hooks** from the egg's `x-raptor.backup` extension ([EGGS.md](EGGS.md#raptor-extensions)), only while the server is running: the `pre` commands (e.g. Minecraft `save-off`, `save-all flush`), then wait up to a minute for the `wait_for` console line, the snapshot, then the `post` commands (`save-on`), **even if the backup failed**. If the game doesn't confirm in time, the backup is still taken and marked with a warning: a possibly inconsistent backup beats none. `wait_for` text shouldn't appear in a pre command itself, in case the game echoes commands.
- **Files that change while being read** (a log being rotated) are skipped rather than failing the backup; the backup notes how many.
- **Restore** is a signed command. It stops the server (it stays stopped if anything fails), takes a **safety backup** of the current files (skipped for an empty directory; kept 7 days, never counted by retention), replaces the directory's contents with the backup, and starts the server again if it was meant to be running. The server is `restoring` meanwhile, and power actions are refused. A backup larger than the server's disk limit is refused before anything changes. A backup can also be restored onto another server when it belongs to a **deleted** server (its final backup, or offsite backups kept after the deletion); that needs an owner's passkey, not a delegation, since a delegate for the new server may never have had access to the old one. Another existing server's backups can't be restored onto a server.
- **File safety:** backups read and restores write only through `os.Root`, confined to the server's directory. Symlinks are backed up as links, never followed. Restored files belong to the server's user and lose setuid, setgid, and sticky bits; nothing is written through an existing entry (the directory is emptied first, and files are created exclusively).
- **The worker process:** Kopia doesn't run inside Wings. Each operation runs in `raptor wings backup-worker`, started by Wings in a transient systemd scope in `raptor-backup.slice` (under `raptor.slice`, beside the game servers) with CPU and I/O weight 10 (games have 100) and `MemoryMax=limits.backup_memory`, and it sets its own OOM score to the maximum. Wings is protected from the OOM killer (`-900`); Kopia's memory inside Wings would make the kernel kill a game server instead. It also dies with Wings, so a resumed job never runs beside an orphan. At most `limits.concurrent_backups` run at once, with two parallel file reads each. As with installs, I/O weight only applies with BFQ or `io.cost`.
- **Disk space:** a backup to the local destination is refused when its filesystem has less than `limits.host_disk_min_free` free.
- **Maintenance:** Wings is each repository's only client and its maintenance owner. Every hour a `backup.maintain` job per destination runs Kopia maintenance when it's due (quick hourly, full daily). Space from deleted backups is freed by full maintenance after Kopia's safety delay, so about a day or two later. The same hourly pass deletes expired safety backups and failed backups older than 7 days.
- **Deleting a server** deletes its local backups, except its **final backup**: with `final_backup`, `server.delete` becomes a job (`server.delete`) that stops the server, backs it up to its destination (the server is `deleting` meanwhile, and power and file access are refused), and only then deletes it. If the backup fails, the server is kept, stopped, with a `server.delete.failed` event. A final backup on the local destination is kept 30 days; offsite ones are kept like the rest (see [SERVERS.md](SERVERS.md#deleting-a-server)).
- **Wipe and reinstall** (`server.reinstall` with `wipe`, signed): the install job takes a safety backup (kept 7 days, like a restore's), removes every file (links, never their targets), and runs the install script; a `server.wiped` event carries the backup's ID. If the backup fails, nothing is removed and the server stays installed.
- **Backups a job takes for itself** (a wipe's safety backup, a deletion's final backup) run inside that job, which already holds the server's lock, rather than as `backup.create` jobs that would wait for it; a resumed job finds its backup again. A deletion counts toward `limits.concurrent_backups`; a wipe's backup counts toward `limits.concurrent_installs` instead. Retention never counts or deletes safety or final backups.
- **Jobs:** `backup.create`, `backup.restore`, and `server.delete` take the server's job lock (so they never overlap each other or an install) and resume after a Wings restart; `backup.delete` and `backup.maintain` are per destination.
- **Events:** `backup.queued`, `backup.finished` (with size, files, new data uploaded, and any warning or error), `backup.deleted` (with the reason: `deleted`, `retention`, `expired`, `server_deleted`), `backup.locked`, `backup.restore.queued`, `backup.restore.finished` (with the safety backup's ID), `backup.policy.updated`, `backup.destination.updated`. Servers add `server.delete.queued`, `server.delete.failed`, and `server.wiped`, and `server.deleted` carries the final backup's ID.
- **Browsing and pulling files out** (`backup.browse`, `backup.extract`, unsigned): a backup can be listed folder by folder, and chosen files and folders restored into a new folder, `.restore/<when the backup was taken>` (`-2`, `-3`, … if taken), while the server runs; nothing it has is replaced. The egg's `file_denylist` applies as it does to the file manager: denied entries are listed locked and never extracted. Extractions are `backup.extract` jobs that take the server's lock (so a full restore can't clear the directory under one) and aren't resumed (the folder would already exist); they're refused over the server's disk limit and emit `backup.extract.finished`.
- **Activity** (`backup.activity`, a read): the server's queued and running backups, restores, and extractions with bytes done of the total, and the last 10 that finished since Wings started, for the Panel's progress bars.
- Tested by unit tests (the engine against a local repository; retention against properties over random histories) and `TestBackupCommands` and `TestWipeAndFinalBackup` (a wipe and a deletion whose backups fail and succeed, and a deleted server's final backup restored onto another server, refused to a delegate) in `task e2e:runtime`: the command path against a real server, the worker's scope and OOM score, and an S3 destination (MinIO).

## Crash detection and restart

Exact rules (what counts as a crash, backoff, crash-loop thresholds) are in [SERVERS.md](SERVERS.md#crash-policy).

- Watches container exits and egg "done"/health signals.
- Restart with backoff. After N crashes in M minutes, stop and mark `crashed` (no infinite restart loops).
- On by default.

## Notifications

Sent **directly from Wings** so they work during Panel outages: Discord webhooks and generic webhooks. (Email will go through the Panel.) Targets are set in `config.yml` **on the box**, never by the Panel: alerts about signed actions exist to catch a compromised Panel, which mustn't be able to silence them.

```yaml
notifications:
  - name: ops                    # optional, for logs and the test command
    type: discord
    url: https://discord.com/api/webhooks/<id>/<token>
    events: [security, crash]    # optional; default: all of them
  - type: webhook
    url: https://example.com/raptor
    secret: <random string>      # optional: signs each request
```

- **Categories:** `security` (every signed action, run, failed, or **rejected**, plus passkey pairings and key resets, from the audit log), `crash` (a crash; a crash loop), `backup` (a failed backup or restore, a restore done, a deletion kept because its final backup failed), `disk` (the host disk low or recovered, a server over its soft disk limit), `install` (a failed install), `update` (a Wings update, or its rollback).
- **Discord:** an embed with the title, details (for crashes, the last console lines), node, and server; colored by level (info, warning, critical); no mentions.
- **Webhooks:** a JSON `POST` (`category`, `level`, `type`, `title`, `text`, `server_id`, `server_name`, `node`, `node_id`, `at`, `data`) with `X-Raptor-Event` and `X-Raptor-Timestamp`, and, with a `secret`, `X-Raptor-Signature: sha256=<hex HMAC-SHA256 of "<timestamp>.<body>">`. Receivers should check the signature and reject old timestamps.
- **Delivery:** Wings reads the event outbox and the audit log with cursors kept in SQLite, so a restart neither loses nor repeats notifications. Signed actions arrive in order, once they've finished. Each send is tried 4 times over about 40 seconds (a 4xx other than 429 isn't retried); a target that still fails is skipped for 5 minutes so it doesn't hold up the others, and what it missed is counted in its next message. At most 20 messages a minute go to each target; the rest are counted and summarized the same way. History from before notifications were configured isn't sent.
- `raptor notifications test` (root) sends a test message to every target and says how each went. Changing targets takes a Wings restart (servers keep running). Webhook URLs are secrets (anyone with a Discord webhook URL can post to it): `raptor doctor` warns if `config.yml` is readable by other users.

## Local metrics

`internal/wings/metrics`: every server is sampled every 10 seconds: CPU (percent of one core), memory (without reclaimable page cache), network traffic, disk use, and players. History is kept in SQLite (`metrics`): one row per minute (average and peak CPU and memory, traffic in the minute, disk at its end, average and peak players) for a day, rolled up into 15-minute rows kept for a week. A stopped server still gets rows (0 samples, its disk). The minute in progress is written when Wings stops, and continued after it starts.

- **Players** come from the game itself, when its egg has `x-raptor.players` ([EGGS.md](EGGS.md#raptor-extensions)): the Minecraft Java status ping or Source's A2S_INFO, every 30 seconds, on the primary port or the one the egg names, with a 2-second timeout. A game that doesn't answer has no count rather than a stale one. Catalog eggs declare it in their `raptor.yaml`; the conformance suite checks it against the real game.
- `GetMetrics` on the local socket returns a server's history (15-minute points older than a day, minute points after) and its latest sample (traffic per second). Streaming to the Panel while someone is watching comes with the node connection (Phase 3).

## Files and SFTP

**Wings runs no HTTP server.** It only listens on the game servers' ports and, when enabled, SFTP.
- **Web file manager:** operations arrive over the node connection as commands (`files.*`), each with the Panel's grant and none passkey-signed (see [SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands)). Paths are from the server's directory; `..` and absolute paths can't leave it.
  - `files.list` (name order, up to 10,000 entries; links show their target and whether it's a file or directory inside), `files.stat`, `files.read` (up to 4 MiB, for the editor; larger files are downloaded). These are read-only commands: they run each time they're sent before they expire, and neither they nor their results are stored with executed commands, so file contents never land in `state.db`.
  - `files.write` (up to 4 MiB; in place, so a link inside the directory is written through and the file keeps its mode; missing parent directories are created), `files.mkdir`, `files.rename` (never over an existing file), `files.copy` ("name copy.ext", then "name copy 2.ext"), `files.delete` (directories with everything in them; links are removed, never their targets), `files.chmod` (permission bits only). Batches stop at the first failure.
  - `files.compress` makes `archive-<UTC time>.tar.gz` next to the files; `files.decompress` extracts `.zip`, `.tar`, `.tar.gz`/`.tgz`, `.tar.bz2`, `.tar.zst`, and single `.gz` files (not `.xz`, `.7z`, or `.rar`), replacing existing files. Both are jobs (`files` class, 2 at a time, one per server alongside installs and backups) whose outcome is a `server.files.compressed`/`server.files.decompressed` event. Archives store links as links and leave out denied and special files; extraction cleans every name, skips what would climb out, special files, and setuid bits, replaces links in its way instead of writing through them, and stops when the server has no disk left (or after a million entries).
  - **Transfers:** `files.upload` (size up front, up to 1 GB, refused if it doesn't fit the server's disk limit) and `files.download` (up to 1 GB) start a transfer; the chunks (up to 64 MiB, under Cloudflare's 100 MB request limit) go over a separate short-lived outbound connection per transfer, which Wings opens when the Panel asks (`OpenTransfer`) and which carries only that transfer, so they never slow the console (see [ARCHITECTURE.md](ARCHITECTURE.md#files-and-sftp)). An upload is written to `.raptor-upload-<id>` in the server's directory (so it counts toward the disk limit; hidden from listings) and moved into place when the last byte arrives; what has arrived is that file's size, so it resumes from there, even after a Wings restart (`files.upload.status`). Uploads idle for a day are dropped. A download can resume from any offset, but not across a change to the file; downloads are kept in memory and expire after an hour idle.
  - Like SFTP, nothing is touched while the server installs or restores; with soft disk limits, what adds data is refused while the server is over its limit.
- **Protected files:** the egg's `file_denylist` is enforced by Wings, for the web file manager and SFTP alike, whatever the Panel sends. Patterns use `.gitignore` syntax, as in Pterodactyl (`*.jar`, `/server.jar`, `config/`, `logs/**`, `!keep.yml`); everything in a denied directory is denied. Denied files are listed (marked as such) but can't be read, written, created, moved, copied, chmodded, deleted, downloaded, or uploaded over, by their own name or through a link that reaches them. Unlike Pterodactyl, a directory can't be moved or deleted while it holds a denied file, nor moved so its files land on denied names, which would otherwise be a way around the list.
- **SFTP** (off by default; the Panel turns it on with `node.sftp` only while someone has a temporary password on the node, and off again after, DECISIONS #225): built-in server (`golang.org/x/crypto/ssh`, with `pkg/sftp` for the protocol), reachable at `n-<short-id>.raptornodes.net`. For files over the web cap and bulk transfers. Only the `sftp` subsystem: no shell, commands, or forwarding.
  - **Username** `user.serverid`: the server's full ID or its short ID (the last 8 characters, as `raptor ps` shows; Pterodactyl uses the first 8, but Raptor's UUIDv7 IDs start with a timestamp that servers created within about a minute of each other share), split at the last dot.
  - **Port:** the one used last, else `ports.sftp` (2022), else the first free port in 2022–2099, never one allocated to a server. Whether SFTP is on and its port are stored in SQLite, so it comes back after restarts. Every change (and every start) records a `node.sftp` event with the port and host key fingerprint.
  - **Auth via the Panel** (`sftp.PanelAuth`, over the node connection with `PanelService.SFTPLogin`). Only temporary passwords the user turned on for that server (checked by the Panel every time, never cached, so logins fail while it's unreachable; the session ends when the password runs out, and `sftp.disconnect` ends a revoked one's sessions; DECISIONS #222, #223). Public keys are refused. Logging in needs the `sftp` permission; reading needs `files.read`, and anything that changes files `files.write`. Every login is recorded as a `server.sftp.login` event.
  - **Limits:** 30 failed logins per address in 5 minutes, then that address is refused until the window ends; 32 connections per address, 512 in total, 8 sessions per connection; 30 s to finish the handshake.
  - **Files:** every operation goes through `os.Root` on the server's directory. FIFOs, device nodes, and sockets are refused (a hostile install can plant them, and Wings runs as root); new files and directories belong to the servers' user with modes 0644/0755; `chmod` sets permission bits only; ownership changes are ignored; symlinks can be created (their target stored as given); plain rename never replaces a file (posix-rename does).
  - Sessions end when an install or a backup restore starts or the server is deleted, and file access is refused while it installs or restores. With soft disk limits, anything that adds data (uploads, new directories and links) is refused while the server is over its limit, but reading, moving, truncating, and deleting still work so space can be freed; with quotas, the kernel enforces the limit.
- SFTP host key: Ed25519, generated on the node at first start (whether or not SFTP is on); its fingerprint is reported to the Panel and shown to users. **Nodes need no TLS certificates.**
- **All file operations use `os.Root`** (symlink-safe, confined to the server directory), in one package (`internal/wings/files`) shared by the web file manager and SFTP.

## CLI scope

The CLI keeps things running. **It never changes server configuration.** All setup happens in the Panel.

```
raptor status                         node health, Panel link, Docker, storage, server counts
raptor doctor [--bundle] [--upload]   diagnose + fix suggestions; support bundle, uploaded for a support code
raptor update [-check] [-version v]   update Wings (root; -check for anyone); Docker too, deliberately (planned)
raptor link --token … | unlink | relink
raptor ps [-json]                     servers: short ID, state, CPU, memory, disk, address, uptime
raptor start|stop|restart|kill <server>   (root) stop waits for the egg's clean shutdown
raptor console <server>               live console; typed or piped lines are sent as commands (root)
raptor logs <server> [-n N] [-f] [-t] output from Docker's log store, further back than the console
raptor backup list [<server>]         backups of a server, or all (with deleted servers' offsite ones)
raptor backup create <server> [-lock]  (root) back up now; waits and prints size and new data
raptor backup restore <server> <id>   (root) asks for the server's name first (or -yes); safety backup first
raptor jobs [logs <id>]
raptor storage status|setup|grow        volume state; create the image volume; grow it online
raptor support status|revoke          see / end active support access
raptor keys list|reset                trusted passkeys for signed actions; reset = re-pair from the box
raptor audit                          signed dangerous actions, from the node's own records
raptor notifications test             a test message to each notification target in config.yml
raptor import pterodactyl             migrate servers from Pterodactyl Wings
raptor uninstall [--wipe-data]
raptor tui                            servers, stats, history graphs, console; power, backup, and command keys (root)
```

Implemented: `status`, `ps`, `start|stop|restart|kill`, `console`, `logs` (Phase 1.7), `storage` (Phase 1.6), `doctor` (Phase 2; `-upload` and the hostname check in Phase 3), `keys`, `audit`, `notifications test`, `tui`, and `import pterodactyl` (Phase 2), `backup` and `update` (Phase 2; `update` for Wings only), `wings run|shutdown-servers` (used by the systemd units), and `wings backup-worker` (started by Wings for each backup operation).

- **`<server>`** is a server's full ID, its **short ID** (the last 8 characters, shown by `ps`; UUIDv7 IDs start with a timestamp that servers created together share, so their ends are used), or its exact name. A name that matches several servers is refused with their IDs.
- **`console`**: shows the history, then live output. On a terminal each line typed is sent as a command, and Ctrl-C or Ctrl-D detaches (the server keeps running). With piped input (`echo "say hi" | raptor console srv`) each line is sent, and it detaches 2 seconds after the last one, so the reply is shown. The same limits as the Panel apply (4 KiB, no line breaks, 10 commands per second per user).
- **`logs`** reads Docker's log store for the server's current container (it survives stops; each start replaces it), so it goes back further than the console's 1,000-line history. `-n -1` prints everything.
- **`backup`**: backups are named by ID or its last 8 characters, as `list` shows them. `create` and `restore` wait until the job is done (`-no-wait` returns once it's queued). `restore` shows what it will replace and asks for the server's name; without a terminal it needs `-yes`. It isn't signed with a passkey like the Panel's restore: root on the box owns the box ([SECURITY-MODEL.md](SECURITY-MODEL.md#trust-boundaries)). Retention settings, locking, deleting backups, and destinations are Panel settings, so they aren't in the CLI.
- Flags can go before or after the server (`raptor logs srv -f`).

### Local socket API

A **small dedicated service**, `raptor.wings.local.v1.LocalService` (in `proto/`), served on `/run/raptor/wings.sock` over Connect (HTTP/1.1 or unencrypted HTTP/2 on the Unix socket). It's not the Panel API, and it has no methods that change server configuration.

Methods are added to the proto as the features behind them are built, so the API never exposes placeholders. Implemented so far: `GetStatus`, `ShutdownServers`, `ListServers`, `Power`, `StreamConsole`, `SendCommand`, `TailLogs`, `ListBackups`, `CreateBackup`, `RestoreBackup`, `Update`, `ListKeys`, `ListAudit`, the key reset (`StartKeyReset`, `GetKeyReset`, `ConfirmKeyReset`, `CancelKeyReset`), `TestNotifications`, `GetMetrics`, and `ImportServer`. `doctor` runs in the CLI itself rather than through the API, so it works while Wings is down. The full planned set:

| Method | Purpose |
|---|---|
| `GetStatus` | Node health, Panel link, Docker, disk, version |
| `ShutdownServers` | Graceful stop of every server for a host shutdown, keeping `desired_state` (root only) |
| `ListServers` | Servers, states, resource usage (CPU measured over 0.5 s, all servers in parallel) |
| `Power` | Start, stop, restart, kill; returns when done (root only) |
| `StreamConsole` (stream), `SendCommand` | Console history and live output; sending commands (root only) |
| `TailLogs` (stream) | Server output from Docker's log store, optionally followed |
| `ListBackups`, `CreateBackup`, `RestoreBackup` | Backups: listing for the `raptor` group; creating and restoring root only, optionally waiting for the job |
| `ListJobs`, `TailJobLogs` (stream) | Jobs and their logs |
| `RunDoctor`, `CreateBundle` | Diagnostics |
| `GetSupportStatus`, `RevokeSupport` | Support access |
| `Link`, `Unlink`, `Relink` | Panel linking |
| `Update` | Self-update: check (anyone with socket access) or install (root); `GetStatus` reports the outcome |
| `ListKeys`, `ListAudit` | Trusted passkeys and delegations; signed actions and key resets (for the `raptor` group) |
| `TestNotifications` | A test message to every notification target (root only) |
| `GetMetrics` | A server's resource history and latest sample (for the `raptor` group) |
| `ImportServer` | A server from another panel, its files copied in (root only) |
| `StartKeyReset`, `GetKeyReset`, `ConfirmKeyReset`, `CancelKeyReset` | Re-pairing the owner's passkey from the box (root only) |

- **Access:** Unix socket permissions: the socket is `0660 root:raptor` (root-only `0600` if the `raptor` group doesn't exist). No passwords or tokens. Members of the `raptor` group can **look** (status, `ps`, console output, logs); anything that changes a server (power actions, console commands, shutting servers down) needs **root**, checked by Wings from the caller's Unix user.
- **Attribution:** Wings reads the caller's Unix user from the socket (`SO_PEERCRED`). Every mutating call is recorded as an event with actor `local:<username>` (power actions as `server.power`, commands as `server.console.command`), so it appears in the Panel's audit log. Power actions from the Panel are recorded the same way with the Panel user.
- **Streams end when Wings shuts down,** so an open `console` or `logs -f` never holds up a Wings restart.
- `raptor storage` works on the box directly, not through the socket, so it also works while Wings is stopped.

## `doctor`

`raptor doctor` (as root) runs on the box without going through Wings, so it works when Wings is down, and asks Wings for its live state when it answers. It never changes anything. Each problem says **what's wrong, why it matters, and how to fix it**; the result is pass, warn (works, but should change), fail (broken, or will break servers), or skip (doesn't apply). It exits 1 if anything failed; `-json` prints the results for scripts and the Panel. Checks:
- OS (Debian 12/13 and Ubuntu 24.04 are supported; others warn), kernel, arch; cgroups v2; systemd, `raptor-wings.service` active, `raptor-shutdown.service` enabled
- The config file; Wings' local API answering and its container runtime ready
- Docker reachable, version (24+), `live-restore` (fail if off: a Docker restart would stop every server), `userland-proxy` off, systemd cgroup driver
- The server data volume mounted with quotas (or soft limits), and its free space
- Host disk free space everywhere Wings and Docker keep state, against `limits.host_disk_min_free` (fail below it, warn below twice it)
- Clock synchronized (NTP). Clock drift breaks connection signatures, grants, and schedules.
- Wings' nftables table in place
- Panel reachable, node key present, and the node connection up (skipped until the node is linked): connecting is a warning, disconnected a failure with Wings' last error and a fix for the usual causes (key revoked or node removed: `raptor relink`; clocks apart; something at the Panel's URL that can't prove it's the Panel)
- The node's hostname (`n-<short id>.raptornodes.net`, saved in `config.yml` when the node links) resolves, and to the address the Panel sees the node connect from (`GET /nodes/address` on the Panel); missing or pointing elsewhere is a warning (skipped on nodes linked by a version that didn't save the hostname)
- SFTP answering as Raptor's SFTP on its port, when it's on
- Pterodactyl Wings on the same box: its Docker network doesn't overlap Raptor's, and its SFTP port isn't Raptor's
- Security warnings (warn only, never changed): password root SSH login, unattended upgrades off
- Wings installed in the self-update layout

`-bundle` writes a redacted `.tar.gz` to `/var/tmp` (root-only): doctor's results, the Wings, shutdown hook, and Docker logs, Docker's version, info, containers, and networks, system state (kernel, memory, disks, mounts, units, the nftables table, addresses), the config file, and the install log and last update if present. **No server files, no secrets:** keys (PEM blocks), values of fields named like secrets (password, token, key…), bearer tokens, and passwords in URLs are replaced with `[redacted]`. `-upload` (which implies `-bundle`) sends it to the Panel over HTTPS (`POST /support/bundles`) and prints a support code like `RPT-7K2M-QX9D` to give to support. A linked node signs the upload with its node key, so support sees which node it's from; nodes that aren't linked, often the ones that need help, can upload too, with tighter limits (#207). If the upload fails, the bundle is still on disk to send another way.

## Updates

`raptor update` (root) installs the newest release in the node's channel, the version pinned in `config.yml`, or the one given with `-version` (which can be older). `raptor update -check` shows what it would install.

The Panel's staged rollouts (5% → 25% → 100%) send **`node.update`** with a version. It's unsigned, because a rollout has no user to sign it, so what it can do is narrow: the same signed-release checks as `raptor update`, only a version **newer** than the running one, and not at all on a node with `updates.automatic: false` or a pinned version, or a development build.

**Rollouts** (`internal/panel/rollout`; `panel rollout start <version>`, `status`, `pause`, `resume`, `cancel`; one at a time): nodes are put in an order that's stable for the rollout and different for each one, so the first 5% isn't always the same boxes. The first 5% of nodes get `node.update`, and the stage runs for an hour after its last update resolves; then 25% for four hours; then everyone. An update resolves as **updated** when the node reconnects on the new version, **failed** if it hasn't 15 minutes after it was sent (it rolled back, or didn't come back), or **skipped** if the node refused (automatic updates off, pinned, development build). Once a tenth of a stage's updates fail, the rollout **halts** until an operator looks. Nodes that are offline get theirs when they reconnect. The engine runs in every `serve api` instance and an advisory lock lets one act at a time.

- **Source:** the project's GitHub releases. `stable` has full releases, `beta` pre-releases too. A channel never moves a node backwards (switching from `beta` to `stable` waits for the next stable release); a pin or `-version` can.
- **Verification**, before anything is run or installed: download `checksums.txt` and `checksums.txt.minisig` → **verify the signature** with the public key built into the binary (`release/minisign.pub`) → check the signature's trusted comment is `raptor <tag> checksums.txt`, so an old signed release can't be served as a new one → download `raptor_linux_<arch>` (at most 256 MiB) → **verify its SHA-256** → run it with `version` and check it's the version it should be.
- **Layout:** versions live side by side as `/usr/local/lib/raptor/raptor-<version>`, with a `current` symlink to the active one; `/usr/local/bin/raptor` links to `current`. Switching versions is one atomic rename of `current`. The previous version is kept; older ones are removed.
- **Trial:** Wings records the update in `/var/lib/raptor/update.json` and exits; systemd (`Restart=always`) starts it again. The version that's still current sees the pending update and **starts the new one as its child** instead of running itself. The child reports healthy once its local API is serving and the container runtime is ready; after another 30 seconds still running, the parent points `current` at it. From then on the new version starts directly, and the old one stays as a small parent process until the next Wings restart. Game servers keep running throughout: they belong to Docker.
- **Automatic rollback:** if the new version exits before it's healthy, or isn't healthy within 5 minutes (the parent stops it), the parent records the failure, removes it, and carries on as Wings itself. `current` never pointed at it. A version that takes the whole box down is covered too: attempts are counted before each start, and after 3 the update is marked failed. Restarting Wings during a trial just starts the next attempt.
- **Reporting:** `raptor update` follows the trial and prints the outcome; `raptor status` shows the last update; the next Wings start records it as a `node.update` event (`result`, `from`, `to`, `actor`, `error`).
- **The trial protocol can never change**, since every version is started by an older one: the child gets `RAPTOR_UPDATE_TRIAL=1` and a pipe on fd 3, and writes `ready\n` to it.
- **Requirements:** Wings must run from `/usr/local/lib/raptor` under systemd (a binary started any other way refuses to update), and the container runtime must be ready, since the new version couldn't be judged healthy otherwise. Only one update at a time.
- SQLite migrations never break the previous minor version's ability to read state (so rollback is safe), or they block rollback explicitly. An older binary opens a database with newer migrations applied without complaint (goose ignores versions it doesn't know).
- On a linked node, the trial's health check also requires the **node connection to come back**: a version that can't reach the Panel is rolled back like one that crashes.

## Pterodactyl import

`raptor import pterodactyl -key <ptla_…>` (root) moves servers from Pterodactyl on the same box:
1. Reads `/etc/pterodactyl/config.yml` (this node's UUID, Wings' token and API port, the data directory, the Panel's URL), finds the node in the Pterodactyl Panel, and lists its servers through the Panel's application API, with the owner's application key (`-key`, or `$RAPTOR_PTERODACTYL_KEY`; never stored). `-server` picks servers (UUID, short ID, or name); `-dry-run` only lists.
2. For each server: rebuild its egg as a PTDL_v2 file from the API (with what it inherits from its parent egg), stop it through Pterodactyl's Wings (killed after 2 minutes), then `ImportServer` on the local socket: Wings creates it **installed** (the install script doesn't run) with the same egg, image, startup, variables, limits, and allocations, and **copies** its directory in through `os.Root` (links kept as links, special files skipped, setuid dropped, everything owned by the servers' user). If the copy fails, the half-made server is removed. `-start` starts it afterwards.
3. The server is then **suspended** in Pterodactyl, so it isn't started there again by mistake (`-no-suspend` skips this; unsuspending it in Pterodactyl undoes it). Pterodactyl's config and files are **never changed or deleted**: copying rather than moving keeps the way back open, at the cost of the disk space until the owner removes the originals. At the end it says how to stop Pterodactyl's Wings.
- Raptor and Pterodactyl coexist on one box (#26): separate networks, ports, labels, and unit names; Raptor never lists or touches containers without its label. `TestPterodactylImport` (in the CI Pterodactyl job) runs a server in the real Pterodactyl beside one in Raptor, restarts Raptor and checks Pterodactyl's server wasn't touched, then imports it and starts it in Raptor on its original port.
