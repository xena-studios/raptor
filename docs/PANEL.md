# Panel

The Panel is Raptor's SaaS control plane: the web app and the API at `raptorpanel.net`. It is **SaaS-only**. The stack is chosen for running it ourselves; there are no concessions or docs for self-hosting.

## Responsibilities

The Panel **owns**: users, orgs, members, roles, auth sessions, permission grants, billing, node identity (public keys) and node DNS, support access grants, the audit log, and the template (egg) catalog.

The Panel **mirrors** (read-only, disposable): servers, schedules, backup index, status, job summaries, and metric summaries, all reported by Wings.

The Panel **never stores**: game files, backup contents, live console, raw logs.

## Process model

One Go binary, one role (see [ARCHITECTURE.md](ARCHITECTURE.md#panel)):
- `panel serve api`: Connect API, WebSockets for browsers and nodes, River jobs. Several instances can run; requests for a node held by another instance are forwarded through Postgres `LISTEN/NOTIFY`.

## Stack

| Concern | Choice |
|---|---|
| Language | Go |
| API | Connect-RPC (protobuf), serves both Connect/gRPC and JSON |
| Database | Postgres, `pgx` + `sqlc` |
| Jobs | River (Postgres-backed) |
| Pub/sub | Postgres `LISTEN/NOTIFY` |
| Auth | WorkOS (AuthKit) |
| Billing | Polar |
| Object storage | S3-compatible object storage (hosted backups, doctor bundles) |
| Frontend | React, TypeScript, Vite, TanStack Router + Query, shadcn/ui, Tailwind, xterm.js, Monaco |
| Observability | `slog` structured logs, OpenTelemetry metrics/traces |

## Data model (sketch)

```
users            id, workos_user_id, email, name, created_at
orgs             id, name, slug, created_at
org_members      org_id, user_id, role (owner|admin|member)
nodes            id, org_id, name, status, cert_serial, cert_expires_at,
                 wings_version, protocol_version, last_seen_at, last_acked_seq,
                 support_access_disabled, billing_state
join_tokens      id, org_id, token_hash, expires_at, used_at
server_grants    user_id, node_id, server_id, permissions[]
support_grants   id, node_id, staff_id, level, reason, ticket_ref,
                 approved_by, expires_at, revoked_at
ssh_keys         id, user_id, public_key, fingerprint
audit_log        id, org_id, actor (user|staff|system), action, target, metadata, at

-- mirror (rebuilt from Wings events)
m_servers        node_id, server_id, name, egg_ref, status, config jsonb, version
m_schedules      node_id, schedule_id, server_id, definition jsonb
m_backups        node_id, backup_id, server_id, size, created_at, destination
m_jobs           node_id, job_id, server_id, type, status, started_at, finished_at

-- billing
billing_customers   org_id, polar_customer_id
billing_ledger      id, org_id, kind, quantity, period, polar_ref, at
subdomains          id, org_id, node_id, server_id, name, zone, created_at
```

IDs are UUIDv7. Every tenant-scoped row carries `org_id`, and Postgres row-level security backs up application-level checks.

## Auth

- **WorkOS AuthKit** for identity: email + password, magic links, social login, passkeys, MFA.
- The Panel keeps its own `users` table keyed by our own ID. The WorkOS ID is a column, so the provider can be changed later.
- After WorkOS authenticates a user, **the Panel issues its own session**. Existing sessions keep working if WorkOS is down.
- Staff accounts are separate from customer accounts and require hardware-key MFA.

## Permissions

- Org roles: `owner`, `admin`, `member`.
- Per-server grants for sub-users (e.g. `console.read`, `console.write`, `power`, `files.read`, `files.write`, `backups`, `schedules`, `startup`, `sftp`, …).
- When a user acts on a node, the Panel sends a **signed, short-lived grant** (≤ 5 min, bound to user + server + action) with the command. Wings verifies the signature and expiry locally.

## Billing (Polar)

- **$12 / node / month.** First node free for the first month.
- Hosted add-ons (backup storage GB-months, relay GB later) are metered usage.
- **Every enrolled node is billed, regardless of status** (online, offline, or unreachable). A node stops being billed only when it's removed from the Panel.
- Implemented as a Polar subscription with a **per-unit quantity** equal to the org's enrolled node count, updated on enroll/removal. Changes are **prorated daily**.
- **Trial:** the first node is free for one month, **once per org**, and a **card is required at signup** (this cuts throwaway re-trial accounts).
- **Hosted backup storage:** $0.02 per GB-month, metered daily from object storage usage.
- The Panel keeps its own **ledger** for reconciliation.
- Non-payment: 7-day grace → the Panel goes read-only for unpaid nodes → **servers keep running**. Hosted backup data is kept 30 days, then deleted after email reminders.

## Support access

1. Staff requests access to a node: level (1 diagnostics / 2 operate / 3 manage), duration, reason, **ticket reference required**.
2. The owner gets an email + Panel notice and approves (may lower level/duration) or denies.
3. The Panel issues a support grant. Wings enforces it like any grant and checks expiry locally.
4. While active: banner in the Panel, notice in `raptor status`. Every staff action goes into the owner's audit log **by staff member name**.
5. Owner can revoke instantly (Panel or `raptor support revoke`). Nodes can disable support access entirely.

**There is no shell access for staff.** Support uses typed diagnostics (`GetDiagnostics`, `RunDoctor`, `TailWingsLogs`, …).

## Beginner-focused features

- **Connection test:** after a server starts (and on demand), the Panel probes the game port from outside. On failure: likely cause + provider-specific guide (Hetzner, OVH, Oracle, AWS, home router).
- **Subdomains:** `<name>.raptornodes.net` with A + SRV records. Reserved names (`www`, `api`, `mail`, `status`, and similar) and anything starting with `n-` are rejected, so player names can never collide with node hostnames (`n-<short-id>.raptornodes.net`). Abuse reporting and bans.
- **Node DNS:** the Panel creates `n-<short-id>.raptornodes.net` at enrollment and updates its A/AAAA records whenever Wings reports a new public IP. Deleted nodes' names are never reused.
- **Safe defaults:** backups on, crash restart on.
- **Node health page:** security and configuration warnings reported by `doctor`.

## AGPL obligations

The Panel footer links to the **exact source commit** that is deployed. All dependencies must be AGPL-compatible (CI checks licenses).

## Hosting

- A primary server, deployed with Docker Compose. The hosting provider is an operational choice and is intentionally not fixed in these docs.
- Postgres with WAL archiving via pgBackRest to **object storage in a different location**, plus a **streaming replica on a second server** at launch (see [RELIABILITY.md](RELIABILITY.md)).
- `raptorpanel.net` is behind the **Cloudflare proxy** (browsers and node connections alike); the origin only accepts traffic from Cloudflare (Authenticated Origin Pulls or an IP allowlist). Caddy terminates TLS at the origin.
- `raptornodes.net` is **DNS-only**, on a plan sized for the record count (see [ARCHITECTURE.md](ARCHITECTURE.md#node-dns)).
- The status page is hosted with a different provider than the Panel.
