# Architecture

## Overview

```
                        *.raptorpanel.net (Cloudflare proxy)
                    ┌──────────────────────────────────────────────────────────────┐
 ┌─────────┐ HTTPS  │  ┌──────────────┐     ┌──────────────────────────────────┐   │
 │ Browser │───────►│  │  app.        │────►│  api.  panel serve api           │   │
 │ (React) │◄──WS───│  │  static SPA  │     │  auth (passkeys, OAuth, email),  │   │
 └─────────┘        │  └──────────────┘     │  billing (Polar), mirror, jobs,  │   │
                    │                       │  node connections (WebSocket)    │   │
                    │                       └──────┬─────────────────▲─────────┘   │
                    │                       ┌──────▼──────┐          │ LISTEN/     │
                    │                       │  Postgres   │──────────┘ NOTIFY      │
                    │                       └─────────────┘                        │
                    └──────────────────────────────▲───────────────────────────────┘
                                                   │ WebSocket (wss://api.raptorpanel.net),
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
A single Go binary, `panel serve api`: the HTTP/Connect API, WebSockets for browsers **and for nodes**, background jobs (River), and billing webhooks. It runs behind the **Cloudflare proxy** on its own origin, `api.raptorpanel.net`, and can run as several instances.

- There's no separate tunnel process. A deploy disconnects every node for a moment; Wings reconnects on its own with random delays (see [Node connection](#node-connection)), and servers are never affected.
- With several instances, a request may land on an instance that doesn't hold the target node's connection. It's forwarded to the instance that does through Postgres (`internal/panel/nodes.Router`): each instance records the nodes it holds (`node_connections`) and says it's alive every 5 seconds (`panel_instances`; one silent for 20 seconds is ignored). A forwarded command is a row in `node_requests` (unlogged); `NOTIFY` on the holder's channel says it's there, and on the sender's that it's answered, with a poll every 2 seconds in case a notification is missed. The holder claims a request before running it, so it runs once; if the holder goes away, the sender retries with the same `command_id` wherever the node reconnects. File transfers still need the instance holding the node (forwarding them comes with the web file manager).

The only infrastructure is **Postgres**. No Redis, no NATS, no message broker.

### Web app
React + TypeScript SPA (Vite, TanStack Router + Query, shadcn/ui + Tailwind, xterm.js, CodeMirror). Talks to the API through generated Connect clients.

It's served as static files **from separate static hosting, not the API servers**, on `app.raptorpanel.net`, and calls the API cross-origin at `api.raptorpanel.net`. The web app is what asks users' passkeys to sign dangerous commands, so compromising the API must not let anyone change it. Deploys need separate credentials, a strict Content Security Policy applies, it loads no third-party scripts, and each release publishes the bundle hashes (see [SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands)).

The API is deliberately **not** served under `/api` on the app's origin: whoever serves a response controls its headers, so a compromised API on that origin could serve a page that asks passkeys to sign, or a service worker that replaces the app (decision 81). In development the Vite dev server still proxies `/api` to the local API.

### Landing page and docs
The landing page (`raptorpanel.net`) is an **Astro** site in `site/`, and the docs (`docs.raptorpanel.net`) will be **Astro Starlight** in `docs-site/`. Both are static, deploy separately from the web app, and never share its origin or CSP. Analytics and marketing scripts live here, never in the web app.

### Hostnames

| Host | Serves | Hosting |
|---|---|---|
| `raptorpanel.net` | Landing page + waitlist (`www` redirects here) | Static, Cloudflare proxy |
| `app.raptorpanel.net` | Web app; passkey RP ID | Static, separate deploy credentials |
| `api.raptorpanel.net` | `panel serve api`: Connect API, browser WebSockets, node connections | Panel servers, Cloudflare proxy |
| `docs.raptorpanel.net` | Docs site | Static |
| `get.raptorpanel.net` | Install script | Static |
| `verify.raptorpanel.net` | The Turnstile page the sign-in form embeds (`web/verify`), apart from the app's origin | Static |
| `status.raptorpanel.net` | Status page | Another provider, DNS not on Cloudflare |
| Mail sending subdomain | SPF/DKIM for transactional email | Resend |
| `n-<short-id>.raptornodes.net`, `<name>.raptornodes.net` | Node hostnames and player subdomains | DNS-only; the apex redirects to `raptorpanel.net` |

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
- On reconnect, the Panel asks for events after `last_acked_seq`. If those events were already pruned (Wings keeps acknowledged events 7 days and at most the newest 100,000 unacknowledged ones) or the Panel's mirror is missing, Wings sends a **full snapshot** and the mirror is rebuilt.
- Events are written in the same SQLite transaction as the change they describe, so the mirror can't miss a change that happened or see one that didn't.
- The mirror is **disposable**: it can be dropped and rebuilt from nodes at any time. **Wings is always right.**
- **Events say what changed; the node says what it is.** Most events carry no state (`server.created` names the server, nothing more), so the Panel (`internal/panel/nodes.Mirror`, one sync at a time per node, coalesced) pulls each batch of events, takes state changes straight from `server.state`, and asks the node for every other server an event names (`NodeService.GetServers`); one the node no longer has is deleted from the mirror. A fetch never replaces a newer version. A snapshot is the same call for every server, with the newest sequence number read before the servers, so the snapshot reflects at least every event up to it. A node without a mirror (`last_acked_seq` = -1) gets a snapshot, as does one the node says is ahead of it or behind what it keeps. A server comes with its schedules, backups, and 50 most recent jobs, which replace the mirror's on every fetch and go when the server does (`m_schedules`, `m_backups`, `m_jobs`). Every change to a server's job's status is an event (`job.status`, written with the change), so the mirror follows jobs from queued to finished. A deleted server's final backup isn't in the mirror; it's listed from the node. Job logs stay on the node.
- Server IDs are **UUIDv7**, generated by Wings.

### Commands are idempotent
Every command carries a `command_id` (UUIDv7). Wings records executed command IDs (kept 7 days) and returns the stored result for duplicates, even after the command expired. The same ID with different content is refused. A retried "create backup" can never produce two backups.

### Commands are signed
Every command carries the Panel's per-user grant. **Dangerous commands** (destroying data, changing code, changing access) also carry the **user's passkey signature over the exact command**, which Wings verifies against keys pinned on the node. The executed-command table doubles as replay protection for those signatures. See [SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands).

## Node connection

**Wings connects out to the Panel and stays connected when it can, but never depends on it.**

```
Wings ──wss://api.raptorpanel.net/nodes/connect──► Cloudflare ──► panel serve api
         (outbound port 443, through NAT/CGNAT)
         yamux streams inside: Panel→Wings RPCs, Wings→Panel RPCs, console, stats
```

- **One WebSocket, opened by Wings**, to `wss://api.raptorpanel.net/nodes/connect` through Cloudflare. It's an ordinary outbound HTTPS connection on port 443, so it works behind home routers, CGNAT, and firewalls. **Nodes open no management port.**
- **Identity, both ways:**
  - *The node proves itself:* at enrollment Wings generates a key pair on the box and the Panel stores the public key. On every connection the Panel sends a random challenge and Wings signs it together with a timestamp and the connection's purpose, so a captured signature can't be replayed. A compromised node's key can be revoked in the Panel. (Client certificates can't be used: Cloudflare terminates TLS, so the Panel never sees them.)
  - *The Panel proves itself:* Wings pins the **Panel's signing key** at enrollment, and the Panel signs its side of the handshake with it. Normal TLS to `api.raptorpanel.net` protects the connection, but because Cloudflare terminates TLS, a pinned server certificate can't be used; the signature is what tells Wings it's talking to the real Panel. The same key signs the per-user command grants Wings verifies (see [SECURITY-MODEL.md](SECURITY-MODEL.md)).
- **Handshake** (`internal/shared/nodelink`), before yamux starts: Wings sends its node ID, protocol version, the connection's purpose, and a random nonce; the Panel answers with its own nonce and a signature over both nonces, the node ID, the purpose, and the time; Wings checks that against the pinned Panel key **before proving anything**, then signs the same fields with its node key. Each side signs the other's fresh nonce, so no signature works on another connection, and the signer is part of what's signed, so one side's signature never passes as the other's. Clocks more than 5 minutes apart are refused with a message saying so. A node the Panel doesn't know (or has removed) is told why and retries with its usual backoff.
- **Multiplexing:** yamux runs inside the WebSocket, so both sides can make calls at the same time on separate streams. The Panel calls Wings (commands, file operations, console), and Wings calls the Panel (event upload, SFTP login checks, IP updates).
- **Protocol:** Connect services defined in `proto/raptor/node/v1` (`NodeService` on Wings, `PanelService` on the Panel). Each yamux stream carries one HTTP/1.1 connection, and both sides run an HTTP server on the streams the other opens, so calls in each direction are ordinary Connect calls.
- **Commands over the connection** (`NodeService.Execute`): the Panel retries a command with the same `command_id` until Wings answers, on the next connection if it drops. A command keeps running on the node if the connection drops while it runs; the retry is told it's still in progress (`ABORTED`) and then gets the stored result. A command that ran and failed is an answer, not an error; refusals (grant, signature, expiry) are errors and aren't retried. Until Wings' container runtime is ready, commands get `UNAVAILABLE` and are retried.
- **Events over the connection:** Wings tells the Panel when it has new events (`PanelService.EventsAvailable`, coalesced) and the Panel pulls them (`NodeService.Events`, after its last sequence number, which also acknowledges everything up to it). A Panel ahead of the node (a `state.db` restored from a snapshot) or behind what's kept gets `FAILED_PRECONDITION` and needs a snapshot.
- **Keepalive:** a yamux ping every 30 seconds from each side, because Cloudflare closes WebSockets that are idle for ~100 seconds. It's a loop inside each process, not a separate one. A side whose pings have gone unanswered for 90 seconds closes the connection (a route that silently stops passing bytes is never noticed otherwise).
- **Reconnect:** Wings retries with exponential backoff and full jitter (1 s → 60 s cap; the backoff resets once a connection has stayed up a minute, so a Panel that accepts and immediately drops a node isn't hammered). Cloudflare drops long-lived WebSockets during its own maintenance and every Panel deploy drops all of them, so reconnecting is routine, and the jitter keeps thousands of nodes from reconnecting in the same second.
- **Not tied to the Panel:** while disconnected, servers keep running, crashes restart, schedules and backups run, and the CLI works. The Panel shows the node as offline and blocks config changes. On reconnect, Wings sends the events the Panel missed ([Mirror sync](#mirror-sync)).
- **Only what's needed flows:** idle, the connection carries only the ping. Console and stats stream only while someone is watching.
- **Version negotiation** at handshake: each side declares its protocol version and capabilities. The Panel supports the last N Wings minor versions.

### Cloudflare in front of the Panel
- `raptorpanel.net` and its subdomains are proxied (orange cloud), except `status.`, which must stay up when Cloudflare doesn't. `raptornodes.net` is **DNS-only** (grey cloud): game traffic and SFTP can't go through the proxy.
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

How the console gets there (built; stats come later):
- **Browser ↔ Panel:** `wss://api.raptorpanel.net/api/live` (`internal/panel/live`), signed in by the session cookie, from the web app's origin only. The browser sends `{"op":"console","id":…,"node":…,"server":…}` to watch a console and `{"op":"close","id":…}` to stop. The Panel answers with `{"id":…,"type":"lines","lines":[…],"reset":true}` (reset: a stream starts over with the history, so the terminal clears) and `{"id":…,"type":"ended","error":…,"retry":true}` when a watch ends (retry: the node is offline; the web app tries again). At most 20 watches per socket. The socket closes when its session ends, checked every minute, and the Panel pings it every 30 seconds, since Cloudflare closes WebSockets idle for 100.
- **Access is checked when a watch opens, and again every 5 minutes**: the Panel reopens the stream (the browser gets the history again with `reset`), so taking away someone's access ends their watch within minutes.
- **Panel ↔ node:** `NodeService.Console`, a server-streaming call over the node connection, with a grant for `server.console` ([SERVERS.md](SERVERS.md#console)).
- **Between Panel instances:** if the other instance holds the node, this one sends it a `con` notification (LISTEN/NOTIFY on its instance channel). The holder opens the stream and sends the batches back as `cl` notifications, split to fit Postgres's 8,000-byte payload; a single line too long for one is cut (lines longer than about 6,000 bytes as JSON, which game output rarely reaches). The watcher renews every 10 seconds (`cka`), the holder stops a stream not renewed for 30 seconds, and `cx` and `ce` close it from either side. Notifications sent one after another arrive in order.

## Files and SFTP

**Wings runs no HTTP server.** The only things listening on a node are the game servers and, if the owner turns it on, SFTP.

| Operation | Path |
|---|---|
| Browse, rename, edit, upload, download (up to **1 GB per file**) | Browser → Cloudflare → Panel → node connection → Wings |
| Anything larger, or bulk transfers | **SFTP**, straight to `n-<short-id>.raptornodes.net` (port shown in the Panel). Never touches the Panel. |
| Hosted backups | Downloaded from backup storage with a short-lived signed link, not through the Panel |

- **Uploads and downloads through the Panel** are sent in chunks under 100 MB (Cloudflare's request limit), resumable, and carried on a **separate short-lived outbound connection** that Wings opens for the transfer, so a large transfer never makes the console lag. Transfers are rate limited per org. Files over the cap show "use SFTP for files this large" with the connection details.
- **The transfer connection:** the Panel calls `OpenTransfer` with the ID of an upload or download a `files.upload` or `files.download` command started; Wings checks it exists, then opens a second WebSocket with the same handshake and the purpose `transfer:<id>`, and answers that transfer's chunks on it (`PUT /upload?offset=`, `GET /download?offset=&n=`, streamed, not buffered) and nothing else. The Panel closes connections it didn't ask for, so a node can't open one on its own. Its yamux window is 16 MiB (the default 256 KiB would cap a transfer at about 5 MB/s at 50 ms), and Wings closes it after 10 minutes unused.
- The cap exists because every byte through the Panel costs bandwidth twice and Cloudflare discourages relaying large non-web files. SFTP has no such cost.
- **SFTP is off by default** and enabled per node in the Panel. With it off, a node has no ports open besides its game servers. It uses SSH host keys, so **nodes need no TLS certificates**.
- **SFTP auth:** SSH keys only (accounts have no passwords). Users add keys in their Security settings and log in as `<sftp username>.<server short ID>`. Wings asks the Panel over the node connection (`PanelService.SFTPLogin`, answered for the node the connection proved it is). Wings **caches the keys the Panel accepts, with their permissions**, so SFTP keeps working while the Panel is unreachable.

## Node DNS

Every node gets a hostname: **`n-<short-id>.raptornodes.net`**, e.g. `n-k7m2qx9d.raptornodes.net`.
- Owners can use it for SFTP and give it to players (`n-k7m2qx9d.raptornodes.net:25565`) instead of an IP.
- The short ID is **random** (not sequential or guessable) and **never reused**: deleted node names stay retired, so old DNS caches and saved addresses never point at someone else's box. The internal node UUID stays the database key.
- **Dynamic DNS:** the Panel takes the node's public address from its connection (`CF-Connecting-IP`, trusted only because the origin accepts nothing but Cloudflare; `PANEL_CLIENT_IP_HEADER`) and updates the A or AAAA record (60 s TTL, DNS-only) whenever it changes, through a Cloudflare API token scoped to the zone (`internal/panel/dns`, `nodes.Addresses`). Only public addresses get records: a node seen from a private or CGNAT address keeps its last public record. The record appears once the node first connects from a public address. A node gets the record for the address family it connects over; a dual-stack box's other family comes when Wings reports its addresses itself (later).
- **Player subdomains** (`smp.raptornodes.net`) share the zone. They can't start with `n-` or use reserved names, so they never collide with node hostnames.
- The hostname points at the owner's real IP, often a home connection without DDoS protection. Players see the IP when they connect anyway; the future connection relay add-on is for owners who want to hide it.
- **Public Suffix List:** `raptornodes.net` is listed before launch, so each subdomain counts as its own registered domain. Otherwise every owner getting a Let's Encrypt certificate for their subdomain shares one rate limit, and one subdomain could set cookies for the others.
- **Record volume:** each node is one record and each player subdomain two (A + SRV). Cloudflare caps records per zone on Free plans, so `raptornodes.net` needs a plan (or DNS provider) with a quota sized for launch. Decide before launch.

## Ports on the node

| Port | Purpose | Direction |
|---|---|---|
| 443 → `api.raptorpanel.net` | Node connection (WebSocket) | Outbound only |
| Allocated game ports | Game traffic | Inbound |
| 2022 (or next free), **only if SFTP is enabled** | SFTP | Inbound |

## Enrollment

1. Owner clicks **Add Node**. The Panel creates a **join token**: single-use, org-bound, expires in 1 hour.
2. The owner's passkey signs that the node linking with this token should trust it (`nodecmd.OwnerPin`; optional, `raptor keys reset` pairs one later), and the owner runs `curl -fsSL https://get.raptorpanel.net | sudo bash -s -- -token rpt_join_…`
3. The bash script (`install/get.sh.tmpl`, filled in by `scripts/make-install-script.sh` when a release is signed, from the checksums just verified against the signing key) checks root, systemd, distro, and architecture, downloads the `raptor` binary, **verifies it against the SHA-256 embedded in the script** (a mismatch installs nothing), installs it as `/usr/local/lib/raptor/raptor-<version>` (linked from `/usr/local/bin/raptor`, [WINGS.md](WINGS.md#updates)), and runs `raptor bootstrap`. All later updates are verified by the binary itself with minisign.
4. `raptor bootstrap` runs preflight checks (systemd, not a container, amd64 or arm64, cgroups v2, Debian 12/13 or Ubuntu 24.04 unless `-force`), installs Docker CE from Docker's repository (held), **merges** Raptor's settings into `daemon.json` (asking before restarting Docker if containers are running), creates the `raptor` user, puts the binary in the update layout, installs the systemd units and a minimal `config.yml`, sets up the server data volume (it explains the trade-offs and asks: an XFS image with hard limits, the default; an XFS data disk; or soft limits), starts Wings, **generates a keypair locally**, and enrolls (`raptor link`): it sends the public key, join token, and hardware facts. `-yes` takes the defaults without asking.
5. The Panel validates and burns the token, stores the node's public key, assigns a node ID and short ID, creates `n-<short-id>.raptornodes.net`, and returns them (the node saves its hostname in `config.yml` for `raptor doctor`'s check).
6. Wings writes `/etc/raptor/config.yml`, installs `raptor-wings.service`, starts, and connects. The Panel UI flips to **Connected**.

Every step is idempotent. Re-running the command after a failure resumes.

**Enrolling** is `raptor link --token` (bootstrap's last step, and usable on its own on a box that already runs Wings): it creates the node key if there's none, signs the token together with its public key (proving it holds the key it enrolls), and calls `EnrollmentService.Enroll` on the Panel's API over HTTPS. The Panel burns the token in the same transaction that creates the node, and remembers which node used it, so repeating an enrollment whose answer was lost, with the same token and key, returns the same node; anyone else with a used token is refused. The answer carries the Panel's signing key, which the node pins: this one call is trust on first use, protected by TLS to the Panel's hostname, and everything after it is verified against the pinned key. `raptor link` writes the key, then `node_id` and `panel.url` into `config.yml` (comments kept), and restarts Wings (servers keep running), which connects.

**Re-linking:** a node whose key was revoked, that was removed from the Panel, or that was unlinked runs `raptor relink` with a new join token from **its own org**, and **keeps its node ID, hostname, and servers**. It always gets a new key (the old one may be why it was revoked), which replaces the old one on the box only once the Panel has it; the Panel drops any connection still using the old key. Node keys don't expire on their own, so a node that was simply offline for a long time reconnects by itself.

**Unlinking:** `raptor unlink` removes the node ID, the node key, and the Panel's key from the box and restarts Wings. Servers keep running and the CLI still works; remote commands are refused. The node stays in the Panel, shown offline, until it's removed there or re-linked.

## Failure modes

| Failure | What happens |
|---|---|
| Panel API down | Servers, schedules, backups, crash restarts keep running. CLI works. SFTP with keys works. Web access unavailable. |
| Panel redeployed | Every node disconnects briefly and reconnects with jitter. Servers unaffected. |
| Cloudflare outage | Same as Panel API down: nodes look offline, the web UI is unavailable, games keep running. |
| Postgres down | Panel down (above). Nodes unaffected. |
| Email provider (Resend) down | Email-code sign-ins, invitations, and security notices fail; passkeys, OAuth, and existing sessions keep working. |
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
  web/                # React app (src/gen = generated TS protobuf); web/verify: the Turnstile page
  deploy/             # the Panel's servers: images, Compose, Caddy, Postgres, scripts (docs/DEPLOY.md)
  site/               # Astro landing page (planned)
  docs-site/          # Astro Starlight docs (planned)
  install/            # get.raptorpanel.net bash script
  tools/              # go.mod pinning dev tools (go tool -modfile=tools/go.mod ...)
  scripts/            # CI/release helper scripts
  release/            # release signing public key
  dev/                # local dev environment (Lima VM config)
  docs/
```
