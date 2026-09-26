# Architecture

## Overview

```
                         raptorpanel.net (Cloudflare proxy)
                    ┌──────────────────────────────────────────────────────────────┐
 ┌─────────┐ HTTPS  │  ┌──────────────┐     ┌──────────────────────────────────┐   │
 │ Browser │───────►│  │  Web app     │────►│  panel serve api                 │   │
 │ (React) │◄──WS───│  │  static SPA  │     │  auth (passkeys, OAuth, email),  │   │
 └─────────┘        │  └──────────────┘     │  billing (Polar), mirror, jobs,  │   │
                    │                       │  node connections (WebSocket)    │   │
                    │                       └──────┬─────────────────▲─────────┘   │
                    │                       ┌──────▼──────┐          │ LISTEN/     │
                    │                       │  Postgres   │──────────┘ NOTIFY      │
                    │                       └─────────────┘                        │
                    └──────────────────────────────▲───────────────────────────────┘
                                                   │ WebSocket (wss://raptorpanel.net),
                                                   │ yamux inside, opened OUTBOUND by Wings
                ┌──────────────────────────────────┼─────────────────────────────┐
                │ Owner's Linux box                │   n-<short-id>.raptornodes.net (DNS only)
                │   ┌──────────────────────────────┴────────────┐                │
                │   │ Wings (raptor-wings.service)              │◄── raptor CLI  │
                │   │ SQLite state · job engine · scheduler     │   (unix socket)│
                │   │ backups (Kopia) · SFTP (opt-in)           │                │
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
A single Go binary, `panel serve api`: the HTTP/Connect API, WebSockets for browsers **and for nodes**, background jobs (River), and billing webhooks. It runs behind the **Cloudflare proxy** on `raptorpanel.net`, and can run as several instances.

- There's no separate tunnel process. A deploy disconnects every node for a moment; Wings reconnects on its own with random delays (see [Node connection](#node-connection)), and servers are never affected.
- With several instances, a request may land on an instance that doesn't hold the target node's connection. It's forwarded to the instance that does through Postgres `LISTEN/NOTIFY`.

The only infrastructure is **Postgres**. No Redis, no NATS, no message broker.

### Web app
React + TypeScript SPA (Vite, TanStack Router + Query, shadcn/ui + Tailwind, xterm.js, Monaco). Talks to the API through generated Connect clients.

It's served as static files **from separate static hosting, not the API servers**, on `raptorpanel.net` (the API lives under `/api` on the same origin, routed by Cloudflare). The web app is what asks users' passkeys to sign dangerous commands, so compromising the API must not let anyone change it. Deploys need separate credentials, a strict Content Security Policy applies, and each release publishes the bundle hashes (see [SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands)).

### Wings
A single Go binary (`raptor`) that is both the daemon (`raptor wings run`) and the CLI. Runs on the owner's box as a systemd service. See [WINGS.md](WINGS.md).

## Data ownership

Every piece of data has **exactly one owner**. Nothing is merged.

| Data | Owner | Other side |
|---|---|---|
| Users, orgs, members, roles | Panel | — |
| Auth, sessions | Panel (built in, passwordless) | — |
| Billing, subscriptions, ledger | Panel (Polar) | — |
| Node identity | Panel stores each node's public key | Wings holds its private key (generated on the box, never leaves it) |
| Node DNS (`n-<short-id>.raptornodes.net`) | Panel | Wings reports its public IP |
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

### Commands are signed
Every command carries the Panel's per-user grant. **Dangerous commands** (destroying data, changing code, changing access) also carry the **user's passkey signature over the exact command**, which Wings verifies against keys pinned on the node. The executed-command table doubles as replay protection for those signatures. See [SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands).

## Node connection

**Wings connects out to the Panel and stays connected when it can, but never depends on it.**

```
Wings ──wss://raptorpanel.net/api/nodes/connect──► Cloudflare ──► panel serve api
         (outbound port 443, through NAT/CGNAT)
         yamux streams inside: Panel→Wings RPCs, Wings→Panel RPCs, console, stats
```

- **One WebSocket, opened by Wings**, to `wss://raptorpanel.net/api/nodes/connect` through Cloudflare. It's an ordinary outbound HTTPS connection on port 443, so it works behind home routers, CGNAT, and firewalls. **Nodes open no management port.**
- **Identity, both ways:**
  - *The node proves itself:* at enrollment Wings generates a key pair on the box and the Panel stores the public key. On every connection the Panel sends a random challenge and Wings signs it together with a timestamp and the connection's purpose, so a captured signature can't be replayed. A compromised node's key can be revoked in the Panel. (Client certificates can't be used: Cloudflare terminates TLS, so the Panel never sees them.)
  - *The Panel proves itself:* Wings pins the **Panel's signing key** at enrollment, and the Panel signs its side of the handshake with it. Normal TLS to `raptorpanel.net` protects the connection, but because Cloudflare terminates TLS, a pinned server certificate can't be used; the signature is what tells Wings it's talking to the real Panel. The same key signs the per-user command grants Wings verifies (see [SECURITY-MODEL.md](SECURITY-MODEL.md)).
- **Multiplexing:** yamux runs inside the WebSocket, so both sides can make calls at the same time on separate streams. The Panel calls Wings (commands, file operations, console), and Wings calls the Panel (event upload, SFTP login checks, IP updates).
- **Protocol:** Connect/gRPC services defined in `proto/`, carried over yamux streams.
- **Keepalive:** a ping every 30 seconds, because Cloudflare closes WebSockets that are idle for ~100 seconds. It's a loop inside Wings, not a separate process. The connection is considered dead after 90 seconds without a reply.
- **Reconnect:** Wings retries with exponential backoff and full jitter (1 s → 60 s cap). Cloudflare drops long-lived WebSockets during its own maintenance and every Panel deploy drops all of them, so reconnecting is routine, and the jitter keeps thousands of nodes from reconnecting in the same second.
- **Not tied to the Panel:** while disconnected, servers keep running, crashes restart, schedules and backups run, and the CLI works. The Panel shows the node as offline and blocks config changes. On reconnect, Wings sends the events the Panel missed ([Mirror sync](#mirror-sync)).
- **Only what's needed flows:** idle, the connection carries only the ping. Console and stats stream only while someone is watching.
- **Version negotiation** at handshake: each side declares its protocol version and capabilities. The Panel supports the last N Wings minor versions.

### Cloudflare in front of the Panel
- `raptorpanel.net` is proxied (orange cloud). `raptornodes.net` is **DNS-only** (grey cloud): game traffic and SFTP can't go through the proxy.
- **Origin locked to Cloudflare** (Authenticated Origin Pulls, or a firewall allowing only Cloudflare's IP ranges), so nobody can reach the Panel around Cloudflare. The client IP header (`CF-Connecting-IP`) is only trusted on connections from Cloudflare.
- A WAF rule lets the node connection path through Cloudflare's bot and challenge features, since Wings isn't a browser.
- Request bodies are limited to 100 MB on Cloudflare's Free and Pro plans, so file uploads through the Panel are sent in chunks.
- **Privacy:** Cloudflare terminates TLS, so it can read what passes through the Panel: console output, commands, and files moved with the web file manager. This is disclosed in the privacy policy. SFTP goes straight to the node and is the private path for files.
- **Dependency:** a Cloudflare outage makes nodes look offline and blocks the web UI. Game servers are unaffected. There's deliberately no non-Cloudflare fallback hostname, because it would expose the origin's IP.

## Realtime (console and stats)

```
Browser ◄─WS─► panel api ◄─(LISTEN/NOTIFY if another instance)─► node connection ◄─yamux─► Wings ◄─► container
```

- **One WebSocket per browser tab**, multiplexing all subscriptions (console for server A, stats for server B, …).
- Stats stream **only while someone is subscribed**. There is no background stats pipeline into Postgres.
- Wings keeps a **ring buffer** of recent console lines per server, so opening the console shows history immediately.
- Slow browser clients get **dropped lines, not backpressure**. A slow browser can never stall a game server's stdout.

## Files and SFTP

**Wings runs no HTTP server.** The only things listening on a node are the game servers and, if the owner turns it on, SFTP.

| Operation | Path |
|---|---|
| Browse, rename, edit, upload, download (up to **1 GB per file**) | Browser → Cloudflare → Panel → node connection → Wings |
| Anything larger, or bulk transfers | **SFTP**, straight to `n-<short-id>.raptornodes.net` (port shown in the Panel). Never touches the Panel. |
| Hosted backups | Downloaded from backup storage with a short-lived signed link, not through the Panel |

- **Uploads and downloads through the Panel** are sent in chunks under 100 MB (Cloudflare's request limit), resumable, and carried on a **separate short-lived outbound connection** that Wings opens for the transfer, so a large transfer never makes the console lag. Transfers are rate limited per org. Files over the cap show "use SFTP for files this large" with the connection details.
- The cap exists because every byte through the Panel costs bandwidth twice and Cloudflare discourages relaying large non-web files. SFTP has no such cost.
- **SFTP is off by default** and enabled per node in the Panel. With it off, a node has no ports open besides its game servers. It uses SSH host keys, so **nodes need no TLS certificates**.
- **SFTP auth:** Wings asks the Panel over the node connection. Wings **caches users' SSH public keys and SFTP permissions** so key-based SFTP keeps working while the Panel is unreachable. Passwords are never cached (the SFTP setup screen says so).

## Node DNS

Every node gets a hostname: **`n-<short-id>.raptornodes.net`**, e.g. `n-k7m2qx9d.raptornodes.net`.
- Owners can use it for SFTP and give it to players (`n-k7m2qx9d.raptornodes.net:25565`) instead of an IP.
- The short ID is **random** (not sequential or guessable) and **never reused**: deleted node names stay retired, so old DNS caches and saved addresses never point at someone else's box. The internal node UUID stays the database key.
- **Dynamic DNS:** Wings reports its public IP (IPv4 and IPv6) over the node connection and the Panel updates the A/AAAA records (low TTL), so boxes on home connections with changing IPs keep working.
- **Player subdomains** (`smp.raptornodes.net`) share the zone. They can't start with `n-` or use reserved names, so they never collide with node hostnames.
- The hostname points at the owner's real IP, often a home connection without DDoS protection. Players see the IP when they connect anyway; the future connection relay add-on is for owners who want to hide it.
- **Record volume:** each node is one record and each player subdomain two (A + SRV). Cloudflare caps records per zone on Free plans, so `raptornodes.net` needs a plan (or DNS provider) with a quota sized for launch. Decide before launch.

## Ports on the node

| Port | Purpose | Direction |
|---|---|---|
| 443 → `raptorpanel.net` | Node connection (WebSocket) | Outbound only |
| Allocated game ports | Game traffic | Inbound |
| 2022 (or next free), **only if SFTP is enabled** | SFTP | Inbound |

## Enrollment

1. Owner clicks **Add Node**. The Panel creates a **join token**: single-use, org-bound, expires in 1 hour.
2. Owner runs `curl -fsSL https://get.raptorpanel.net | sudo bash -s -- --token rpt_join_…`
3. The bash script checks root, systemd, distro, and architecture, downloads the `raptor` binary, **verifies it against the SHA-256 embedded in the script** (the script is generated per release), and runs `raptor bootstrap`. All later updates are verified by the binary itself with minisign.
4. `raptor bootstrap` runs preflight checks, installs and configures Docker, sets up the quota volume, creates the `raptor` system user and directories, **generates a keypair locally**, and enrolls: it sends the public key, join token, and hardware facts.
5. The Panel validates and burns the token, stores the node's public key, assigns a node ID and short ID, creates `n-<short-id>.raptornodes.net`, and returns them.
6. Wings writes `/etc/raptor/config.yml`, installs `raptor-wings.service`, starts, and connects. The Panel UI flips to **Connected**.

Every step is idempotent. Re-running the command after a failure resumes.

**Re-linking:** a node whose key was revoked, or that was removed from the Panel, runs `raptor relink` with a new join token (or proves its identity with its old key if it's still trusted) and **keeps its node ID, hostname, and servers**. Node keys don't expire on their own, so a node that was simply offline for a long time reconnects by itself.

## Failure modes

| Failure | What happens |
|---|---|
| Panel API down | Servers, schedules, backups, crash restarts keep running. CLI works. SFTP with keys works. Web access unavailable. |
| Panel redeployed | Every node disconnects briefly and reconnects with jitter. Servers unaffected. |
| Cloudflare outage | Same as Panel API down: nodes look offline, the web UI is unavailable, games keep running. |
| Postgres down | Panel down (above). Nodes unaffected. |
| Email provider down | Email-code sign-ins fail; passkeys, OAuth, and existing sessions keep working. |
| Google / Discord / GitHub down | That provider's sign-in fails; other methods and existing sessions work. |
| Polar down | Billing actions fail. Nothing else is affected. |
| Node connection drops mid-command | The command is retried with the same `command_id`. No duplicates. |
| Node's public IP changes | Wings reports the new IP; the Panel updates `n-<short-id>.raptornodes.net`. |
| Wings crashes or updates | **Containers keep running** (they belong to Docker). Wings reattaches on start. |
| Docker restarts | `live-restore` keeps containers running. |
| Box shuts down | `raptor-shutdown.service` gracefully stops every server (egg stop command) before Docker stops. |
| Box reboots | Wings verifies the quota volume is mounted, then starts every server whose desired state is `running`, staggered. Docker doesn't auto-restart containers. |
| Box dies permanently | Panel mirror has the config. Offsite backups (key held by the Panel by default) restore onto a new node. |
| Node's SQLite corrupt | Restore from local snapshot (`VACUUM INTO`) or offsite copy. Worst case: rebuild from the Panel mirror + backups. |
| Owner stops paying | Panel goes read-only for unpaid nodes. Servers keep running. |

## Monorepo layout

```
raptor/
  proto/              # protobuf: node connection, public API, local socket API
  cmd/
    panel/            # Panel binary (serve api; migrate)
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
