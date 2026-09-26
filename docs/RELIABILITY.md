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
- Signature-verified download → atomic swap → restart → health check.
- **Automatic rollback** if the new version doesn't start or doesn't reconnect within 5 minutes.
- Staged rollout (5% → 25% → 100%) with automatic halt if error rates rise.

### State durability
- SQLite in WAL mode, a single writer connection, `busy_timeout`, `synchronous=NORMAL`.
- Hourly `VACUUM INTO` snapshots (last 24 kept), and a snapshot before every migration.
- The latest snapshot is included in offsite backups.
- **Host disk protection:** Wings watches free space on the host disk (where SQLite, Docker images, and logs live). Below a threshold it refuses new installs and image pulls and alerts, so the node's own state never ends up on a full disk.

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

- One WebSocket per node through Cloudflare, multiplexed with yamux. Ping every 30 s (Cloudflare closes WebSockets idle for ~100 s); dead after 90 s without a reply.
- **Reconnect with full jitter** (1 s → 60 s cap) so thousands of nodes don't reconnect at the same moment. Reconnects are routine: every Panel deploy and Cloudflare's own maintenance drop long-lived WebSockets.
- **Head-of-line blocking:** all streams share one TCP connection, so a large transfer could delay console and control traffic. Rules:
  - File transfers never use the main connection: Wings opens a **separate short-lived outbound connection** per transfer.
  - Separate streams for control, console, and stats, with per-stream flow control windows.
- Command idempotency via `command_id` makes retries after drops safe.
- Capacity: a Go process holds tens of thousands of idle WebSockets comfortably (tens of KB each). At launch scale this is not a constraint.

## Panel

### Deploys
The Panel is one process role (`serve api`), deployed with zero downtime for browsers (start new → health check → shift traffic → drain old).
- Node connections move to the new instances during the drain: the old instance asks its nodes to reconnect, spread over a short window, before it exits. Open consoles reconnect the same way.
- **Accepted trade-off:** every deploy briefly reconnects every node. Game servers are never affected (they don't depend on the connection), and jittered reconnects avoid a storm. This replaced the separate `tunnel` process of the original design (decision 72).

### Database
- **Streaming replica on a second server from launch.** A single Postgres box is a single point of failure for logins and management. Promoting a replica takes minutes; restoring from a WAL archive can take much longer. A second box is cheap compared to the cost of a long outage.
- WAL archiving (pgBackRest) to object storage **in a different location**. Restores are **tested monthly**.
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

- **Fault-injection tests:** drop the node connection mid-command, kill Wings mid-job, kill Docker, reboot the VM, fill the host disk, fill the quota volume, unmount the quota volume, corrupt `state.db`. Expected behavior is asserted, not assumed.
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
