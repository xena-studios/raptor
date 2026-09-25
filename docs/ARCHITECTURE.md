# Architecture

## Overview

```
                    ┌─────────────────────────── RAPTOR PANEL ──────────────────────┐
                    │                                                               │
 ┌─────────┐ HTTPS  │  ┌──────────────┐     ┌──────────────────────────────────┐    │
 │ Browser │───────►│  │  Web app     │────►│  panel (role: api)               │    │
 │ (React) │◄──WS───│  │  static SPA  │     │  auth (WorkOS), orgs, grants,    │    │
 └─────────┘        │  └──────────────┘     │  billing (Polar), mirror, jobs   │    │
                    │                       └──────┬─────────────────▲─────────┘    │
                    │                              │ Postgres        │ LISTEN/      │
                    │                       ┌──────▼──────┐          │ NOTIFY       │
                    │                       │  Postgres   │──────────┘              │
                    │                       └──────▲──────┘                         │
                    │                              │                                │
                    │                       ┌──────┴───────────────────────────┐    │
                    │                       │  panel (role: tunnel)            │    │
                    │                       │  holds one mTLS conn per node    │    │
                    │                       └──────▲───────────────────────────┘    │
                    └──────────────────────────────┼────────────────────────────────┘
                                                   │ mTLS over TCP 443, multiplexed (yamux)
                                                   │ opened OUTBOUND by Wings
                ┌──────────────────────────────────┼─────────────────────────────┐
                │ Owner's Linux box                │                             │
                │   ┌──────────────────────────────┴────────────┐                │
                │   │ Wings (raptor-wings.service)              │◄── raptor CLI  │
                │   │ SQLite state · job engine · scheduler     │   (unix socket)│
                │   │ backups (Kopia) · SFTP · HTTPS files      │                │
                │   └─────────────────┬─────────────────────────┘                │
                │                  Docker                                        │
                │          ┌──────┐ ┌──────┐ ┌──────┐                            │
                │          │ mc1  │ │ rust │ │ bot  │                            │
                │          └──▲───┘ └──▲───┘ └──────┘                            │
                └─────────────┼────────┼─────────────────────────────────────────┘
                              │        │
                     Players connect directly (game traffic never touches the Panel)
```

## Components

### Panel
A single Go codebase and binary, run as **two process roles**:

| Role | Responsibility | Deploy cadence |
|---|---|---|
| `panel serve api` | HTTP/Connect API, WebSockets for the browser, background jobs (River), billing webhooks | Often |
| `panel serve tunnel` | Accepts and holds Wings connections, routes RPCs to nodes | Rarely |

Both roles share the same code and database. They are separate processes so that **deploying the API doesn't disconnect every node** (see [RELIABILITY.md](RELIABILITY.md)). They talk to each other through Postgres `LISTEN/NOTIFY` and an internal RPC endpoint.

The only infrastructure is **Postgres**. No Redis, no NATS, no message broker.

### Web app
React + TypeScript SPA (Vite, TanStack Router + Query, shadcn/ui + Tailwind, xterm.js, Monaco). Served as static files. Talks to the API through generated Connect clients.

### Wings
A single Go binary (`raptor`) that is both the daemon (`raptor wings run`) and the CLI. Runs on the owner's box as a systemd service. See [WINGS.md](WINGS.md).

## Data ownership

Every piece of data has **exactly one owner**. Nothing is merged.

| Data | Owner | Other side |
|---|---|---|
| Users, orgs, members, roles | Panel | — |
| Auth, sessions | Panel (WorkOS for identity) | — |
| Billing, subscriptions, ledger | Panel (Polar) | — |
| Node identity, certificates | Panel (internal CA) | Wings holds its own key + cert |
| Permission grants (user → server → perms) | Panel | Wings enforces signed grants; caches SFTP public keys |
| Servers, config, allocations | **Wings** | Panel keeps a read-only **mirror** |
| Schedules, backup config, backup index | **Wings** | mirrored |
| Jobs, job logs | **Wings** | summaries mirrored |
| Game files, console, logs | **Wings** | live passthrough only, never stored |
| Metrics | **Wings** (~7 days local) | downsampled summaries mirrored |

### Single writer
The Panel **never writes server data directly**. It sends a **command** to Wings. Wings validates it, applies it, stores it in SQLite, and emits an **event**. The Panel updates its mirror from the event.

```
Browser ──► Panel: permission check ──► command (with command_id) ──► Wings
                                                                        │ apply, persist
Panel mirror ◄── event (node seq N) ◄───────────────────────────────────┘
```

- The CLI only performs **actions** (power, backup, restore). It never changes config. So config only ever originates from the Panel, and there is nothing to merge.
- When a node is offline, the Panel **blocks config edits** and shows the node as unreachable. (Queued edits may come later.)

### Mirror sync
- Wings keeps an **append-only event log** with a per-node monotonic sequence number.
- The Panel stores `last_acked_seq` per node.
- On reconnect, the Panel asks for events after `last_acked_seq`. If the gap is too large or the Panel's mirror is missing, Wings sends a **full snapshot** and the mirror is rebuilt.
- The mirror is **disposable**: it can be dropped and rebuilt from nodes at any time. **Wings is always right.**
- Server IDs are **UUIDv7**, generated by Wings.

### Commands are idempotent
Every command carries a `command_id` (UUIDv7). Wings records executed command IDs (retained ≥ 24h) and returns the stored result for duplicates. A retried "create backup" can never produce two backups.

## The tunnel

- **Wings dials out** to `tunnel.raptorpanel.net:443`. Owners open no management port.
- **mTLS**: Wings authenticates with a client certificate issued at enrollment. The Panel authenticates with its server certificate, pinned by Wings.
- **Multiplexing**: yamux over the single TLS connection. The Panel acts as an RPC **client** on streams it opens toward Wings. Wings acts as a client on streams it opens toward the Panel (event upload, grant checks).
- **Protocol**: Connect/gRPC services defined in `proto/`, carried over yamux streams.
- **Streams by priority**: control RPCs, console, and stats each use their own streams. Bulk data over the tunnel is capped (see [RELIABILITY.md](RELIABILITY.md)); large files go direct.
- **Heartbeat**: every 15s both ways. Dead after 45s.
- **Reconnect**: exponential backoff with full jitter (1s → 60s cap), so a Panel restart doesn't cause a thundering herd.
- **Version negotiation** at handshake: each side declares its protocol version and capabilities. The Panel supports the last N Wings minor versions.

## Realtime (console and stats)

```
Browser ◄─WS─► panel api ◄─internal─► panel tunnel ◄─yamux─► Wings ◄─► container stdio
```

- **One WebSocket per browser tab**, multiplexing all subscriptions (console for server A, stats for server B, …).
- Stats stream **only while someone is subscribed**. There is no background stats pipeline into Postgres.
- Wings keeps a **ring buffer** of recent console lines per server, so opening the console shows history immediately.
- Slow browser clients get **dropped lines, not backpressure**. A slow browser can never stall a game server's stdout.

## Files and SFTP

Heavy file traffic goes **directly to the box**, never through the Panel.

| Operation | Path |
|---|---|
| Browse, rename, small edits (≤ 10 MB) | Browser → Panel → tunnel → Wings |
| Large upload/download | Browser → `https://<node-id>.node.raptornodes.net:8443` with a short-lived signed token issued by the Panel |
| SFTP | Client → `<node-id>.node.raptornodes.net:2022` (or the port shown in the Panel) |

- **TLS for nodes:** the Panel obtains certificates for `<node-id>.node.raptornodes.net` from Let's Encrypt using **DNS-01** (Raptor controls the zone) and delivers them to Wings over the tunnel. Owners configure nothing, and no port 80 is needed.
- **SFTP auth:** Wings asks the Panel over the tunnel. Wings **caches users' SSH public keys and SFTP permissions** so key-based SFTP keeps working while the Panel is unreachable. Passwords are never cached.

## Ports on the node

| Port | Purpose | Direction |
|---|---|---|
| 443 → `tunnel.raptorpanel.net` | Tunnel | Outbound only |
| Allocated game ports | Game traffic | Inbound |
| 2022 (or next free) | SFTP | Inbound |
| 8443 (or next free) | Direct file transfer (HTTPS) | Inbound |

## Enrollment

1. Owner clicks **Add Node**. The Panel creates a **join token**: single-use, org-bound, expires in 1 hour.
2. Owner runs `curl -fsSL https://get.raptorpanel.net | sudo bash -s -- --token rpt_join_…`
3. The bash script checks root, systemd, distro, and architecture, downloads the `raptor` binary, **verifies it against the SHA-256 embedded in the script** (the script is generated per release), and runs `raptor bootstrap`. All later updates are verified by the binary itself with minisign.
4. `raptor bootstrap` runs preflight checks, installs and configures Docker, sets up the quota volume, creates the `raptor` system user and directories, **generates a keypair locally**, and enrolls: it sends the public key, join token, and hardware facts.
5. The Panel validates and burns the token, signs a client certificate, and returns the node ID + cert + Panel CA.
6. Wings writes `/etc/raptor/config.yml`, installs `raptor-wings.service`, starts, and opens the tunnel. The Panel UI flips to **Connected**.

Every step is idempotent. Re-running the command after a failure resumes.

**Re-linking:** a node offline long enough for its cert to expire runs `raptor relink` (proves identity with its old key, or accepts a new join token) and **keeps its node ID and servers**.

## Failure modes

| Failure | What happens |
|---|---|
| Panel API down | Servers, schedules, backups, crash restarts keep running. CLI works. SFTP with keys works. Web access unavailable. |
| Panel tunnel process down / redeployed | Same as above. Wings reconnects with jitter when it's back. |
| Postgres down | Panel down (above). Nodes unaffected. |
| WorkOS down | New logins fail. Existing Panel sessions keep working (sessions are issued by the Panel). |
| Polar down | Billing actions fail. Nothing else is affected. |
| Tunnel drops mid-command | The command is retried with the same `command_id`. No duplicates. |
| Wings crashes or updates | **Containers keep running** (they belong to Docker). Wings reattaches on start. |
| Docker restarts | `live-restore` keeps containers running. |
| Box reboots | Docker restarts containers per Wings' restart policy. Wings verifies the quota volume is mounted before starting anything. |
| Box dies permanently | Panel mirror has the config. Offsite backups (key held by the Panel by default) restore onto a new node. |
| Node's SQLite corrupt | Restore from local snapshot (`VACUUM INTO`) or offsite copy. Worst case: rebuild from the Panel mirror + backups. |
| Owner stops paying | Panel goes read-only for unpaid nodes. Servers keep running. |

## Monorepo layout

```
raptor/
  proto/              # protobuf: tunnel, public API, local socket API
  cmd/
    panel/            # Panel binary (roles: api, tunnel; migrate)
    raptor/           # Wings daemon + CLI + TUI
  internal/
    gen/proto/        # generated Go protobuf + Connect code
    panel/...         # Panel packages (api, store, ...)
    wings/...         # Wings packages (store, ...)
    eggs/             # egg parsing + runtime (used by Wings)
    shared/...        # shared Go packages (buildinfo, ...)
  db/
    panel/            # Postgres migrations + sqlc queries
    wings/            # SQLite migrations + sqlc queries
  web/                # React app (src/gen = generated TS protobuf)
  install/            # get.raptorpanel.net bash script
  tools/              # go.mod pinning dev tools (go tool -modfile=tools/go.mod ...)
  scripts/            # CI/release helper scripts
  release/            # release signing public key
  dev/                # local dev environment (Lima VM config)
  docs/
```
