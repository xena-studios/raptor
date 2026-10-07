# Reliability and Performance

Raptor's promise is **reliable**, so reliability is a product requirement, not an ops afterthought. This document sets the targets and the rules that meet them.

## The most important property

**A game server's uptime depends only on the box it runs on.** The Panel, the node connection, Cloudflare, the email provider, OAuth providers, Polar, Wings restarts, Wings updates, and Docker restarts can all fail or restart without a game server going down. Every design choice is checked against this.

## Targets

| Target | Value |
|---|---|
| Game server downtime caused by Raptor (Panel, Wings restart/update) | **0** |
| Panel API availability | 99.9% monthly |
| Panel data loss on disaster (RPO) | ≤ 1 minute (streaming replica), ≤ 5 minutes worst case (WAL archive) |
| Panel recovery time (RTO) | ≤ 30 minutes (promote replica) |
| Console latency (box → browser) | p95 < 250 ms added by Raptor |
| Panel API latency | p95 < 150 ms for reads |
| Node reconnect after a Panel restart | All nodes reconnected < 2 min, without load spikes |
| Wings idle footprint | < 50 MB RAM, ~0% CPU |

## Wings

### Wings restarts never touch servers
- Containers are owned by Docker. Wings restarting, crashing, or updating **does not stop them**.
- On start, Wings **reattaches**: lists `raptor.wings.managed` containers, reconciles against SQLite, reattaches console streams, and refills the console ring buffer from Docker logs.
- Docker `live-restore` keeps containers running across Docker restarts.
- The Docker package is held so unattended upgrades can't restart it.

### Updates can't break a node
- Signature-verified download → restart → the running version starts the new one on trial → atomic swap only once it's healthy ([WINGS.md](WINGS.md#updates)).
- **Automatic rollback** if the new version crashes or isn't healthy within 5 minutes (Phase 3 adds: doesn't reconnect). The old version supervises the trial, so a broken new version can't block its own rollback.
- Staged rollout (5% → 25% → 100%) with automatic halt if error rates rise.

### State durability
- SQLite in WAL mode, a single writer connection, `busy_timeout`, `synchronous=NORMAL`.
- Hourly `VACUUM INTO` snapshots (last 24 kept), one on every clean Wings stop (last 3), and one before every migration.
- **A corrupt `state.db` is replaced automatically:** Wings runs SQLite's `quick_check` when it opens the database; if it fails (or the file isn't a database), the damaged file and its WAL are moved aside (`state.db.corrupt-<time>`, never deleted), the newest snapshot that passes the check is restored, and Wings starts on it with a `node.state_restored` event and a critical notification. Changes since that snapshot are lost: at most an hour's, nothing after a clean stop. Running servers are reattached as usual. If no snapshot passes, Wings refuses to start with a clear error rather than start empty and forget servers whose containers are still running.
- The latest snapshot is included in offsite backups.
- **Host disk protection:** Wings watches free space everywhere it and Docker keep state (Docker's data directory, `state.db`, logs, tmp, and local backups), every minute and before each install or pull. Below `limits.host_disk_min_free` (default 10 GiB) on any of them, it refuses new servers, reinstalls (including installs queued before the disk ran low, which leave the server as it was), and pulls of images the node doesn't have, so the node's own state never ends up on a full disk. A server whose image is already here still starts on that image: refusing to start games would make a low disk worse, not better. Going low and recovering are `node.disk_low` and `node.disk_ok` events and log lines, and `raptor status` shows the free space. Local backups have the same floor (see [WINGS.md](WINGS.md#backups)).

### Game server performance
- **CPU:** default to CPU **weight**, not hard CFS quotas. Hard quotas cause throttling stalls that show up as tick lag. A hard limit and CPU pinning are per-server options.
- **Memory:** container limit = allocated + overhead, so JVM servers aren't OOM-killed at the edge of their heap. Game containers live in `raptor.slice` with a ceiling of total RAM minus a reserve (10% of RAM, 1–4 GiB) for the OS, Docker, and Wings. Wings has `OOMScoreAdjust=-900`.
- **Background work can't cause lag:** backups, installs, and updates run with low CPU and I/O weight, with global concurrency limits (e.g. 2 backups per node).
- **Networking:** `userland-proxy` off (kernel forwarding, important for UDP). Host networking available per server.
- **Disk usage reporting is free:** XFS project quotas give instant usage. No `du` scans walking millions of files.
- **Console never blocks the game:** stdout is drained continuously into a ring buffer. Slow viewers get dropped lines, never backpressure.

### Resilient jobs
- Durable queue, resume/retry with backoff, per-server locks, global limits.
- Image pulls and install downloads: timeouts, retries, progress reporting.
- Crash loops are detected and stopped (N crashes in M minutes → `crashed`).

## Node connection

- One WebSocket per node through Cloudflare, multiplexed with yamux. Ping every 30 s (Cloudflare closes WebSockets idle for ~100 s); dead after 90 s without a reply, on both sides.
- A command whose connection drops keeps running on the node; the Panel retries it with the same `command_id` on the next connection and gets its result. Tested by cutting the connection mid-command (`internal/wings/link`), and a silently dead route by a proxy that stops forwarding (`internal/shared/nodelink`).
- **Reconnect with full jitter** (1 s → 60 s cap) so thousands of nodes don't reconnect at the same moment. Reconnects are routine: every Panel deploy and Cloudflare's own maintenance drop long-lived WebSockets.
- **Head-of-line blocking:** all streams share one TCP connection, so a large transfer could delay console and control traffic. Rules:
  - File transfers never use the main connection: Wings opens a **separate short-lived outbound connection** per transfer.
  - Separate streams for control, console, and stats, with per-stream flow control windows.
- Command idempotency via `command_id` makes retries after drops safe.
- Capacity: a Go process holds tens of thousands of idle WebSockets comfortably (tens of KB each). At launch scale this is not a constraint.

## Panel

### Deploys
The Panel is one process role (`serve api`), deployed with zero downtime for browsers (start new → health check → shift traffic → drain old). Server #1 runs two instances behind Caddy, and `deploy/scripts/deploy.sh` runs migrations (always compatible with the running version), then replaces one instance at a time, waiting for each to be healthy. A Panel shutting down answers 503 on `/healthz` first, so Caddy stops sending it requests while it drains. The rehearsal (`task deploy:rehearse`) probes the API every 100 ms through a deploy and fails on one error.
- Node connections move to the new instances during the drain: on SIGTERM the old instance stops claiming its nodes (so commands go elsewhere at once), turns new node connections away (503), and closes the ones it has spread evenly over 20 seconds, so they reconnect to the other instances a few at a time. Open consoles reconnect the same way. Tested with two instances on one database (`TestRouter`): commands through either reach the node, and draining one moves the node to the other without a command running twice.
- **Accepted trade-off:** every deploy briefly reconnects every node. Game servers are never affected (they don't depend on the connection), and jittered reconnects avoid a storm. This replaced the separate `tunnel` process of the original design (decision 72).

### Database
- **Streaming replica on a second server from launch.** A single Postgres box is a single point of failure for logins and management. Promoting a replica takes minutes; restoring from a WAL archive can take much longer. A second box is cheap compared to the cost of a long outage.
- WAL archiving (pgBackRest) to object storage **in a different location**. Restores are **tested monthly**, by a timer: the latest backup is restored into a scratch container and compared with the primary (`deploy/scripts/restore-test.sh`).
- **Failover is manual** (#205): promote the replica, start the Panels on server #2, move the `api` record ([DEPLOY.md](DEPLOY.md#failover)). Automatic failover with two database servers can't tell a dead primary from a split network, and two primaries is worse than a few minutes down.
- Connection pooling via `pgxpool`. Every query has a context timeout.
- Event ingestion from Wings is **batched** (multi-row inserts / `COPY`), not one transaction per event.
- No live stats in Postgres. Only downsampled summaries.

### Request handling
- Timeouts and context cancellation on every request, RPC, and query.
- Rate limits per user, per org, and per node.
- Graceful degradation: if a node is offline, pages render from the mirror with a clear **stale** state. They never hang waiting for a node.

### Frontend
- Route-based code splitting; the console (xterm.js) and editor (Monaco) load lazily.
- TanStack Query caching with mirror data, so page navigation is instant.
- One WebSocket per tab, multiplexing all subscriptions.
- The UI shows **live / stale / pending / failed** states explicitly. Stale data never looks live.

## Observability

Reliability you can't see isn't reliability.
- **Panel:** structured logs (`slog`), OpenTelemetry metrics and traces, dashboards and alerts for API error rates and latency, node connection counts, reconnect rates, event lag per node, job queue depth, and Postgres replication lag.
- **Wings:** its own health metrics (reconnects, job failures, crash loops, disk pressure) are reported over the node connection, visible to the owner on the node health page and to us in aggregate.
- **Status page** (`status.raptorpanel.net`) hosted with a different provider than the Panel, with DNS that doesn't depend on Cloudflare, so it stays up during a Cloudflare outage.
- Alerting that pages a human for: API down, Cloudflare or node connections failing, replication broken, backup archive failing, mass node disconnects.

## Testing for reliability

- **Fault-injection tests** (`task e2e:faults`, in a throwaway VM): kill Wings (SIGKILL) mid-backup (the job resumes and finishes; the server never restarts), kill Docker (the server keeps running under live-restore; Wings reattaches its console), fill the host disk to below the threshold and then completely (backups refused, Wings and servers keep running, recovered after freeing it), fill a server's quota (its writes fail; Wings unaffected), unmount the data volume (starts refused with a clear error, nothing written underneath, recovered on remount), corrupt `state.db` (restored from the shutdown snapshot; the server keeps running and is still known). Expected behavior is asserted, not assumed. Reboots are in `e2e:host`; dropping the node connection mid-command comes with the connection (Phase 3).
- **Soak test** (`task soak:start`, in its own VM, then `soak:report` and `soak:stop`): servers with scheduled restarts (every 6 h) and backups (every 2 h), Wings restarted at random every 1 to 6 hours, and a monitor recording every server's container each minute. The report counts container restarts against the restarts their schedules asked for, samples where a server was down, and failed backups; anything unscheduled fails it. Phase 2 exits after a week of it with no unexpected downtime.
- **Load tests:** a fake-Wings simulator opening thousands of node connections through Cloudflare and streaming events, run against the Panel before launch and before major releases.
- **Install matrix:** Debian 12, Debian 13, Ubuntu 24.04 × amd64/arm64, full install end to end in CI on real VMs.
- **Upgrade tests:** upgrade from each supported previous Wings version, then roll back.
- **Egg conformance suite:** see [EGGS.md](EGGS.md).

## What we deliberately don't need

These would add complexity without making Raptor more reliable at its scale:
- Kubernetes, a service mesh, or microservices
- Redis, NATS, Kafka
- Multi-region Panel (servers don't depend on the Panel being up)
- Distributed consensus between nodes

Revisit only when measurements say so.
