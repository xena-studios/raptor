# Panel

The Panel is Raptor's SaaS control plane: the web app at `app.raptorpanel.net` and the API at `api.raptorpanel.net`. It is **SaaS-only**. The stack is chosen for running it ourselves; there are no concessions or docs for self-hosting.

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
| Auth | Built into the Panel, passwordless: passkeys (`go-webauthn/webauthn`), OAuth (`golang.org/x/oauth2`, `coreos/go-oidc`), email codes, TOTP (`pquerna/otp`) |
| Email | Transactional email provider (login codes, notifications) |
| Billing | Polar |
| Object storage | S3-compatible object storage (hosted backups, doctor bundles) |
| Frontend | React, TypeScript, Vite, TanStack Router + Query, shadcn/ui, Tailwind, xterm.js, Monaco |
| Observability | `slog` structured logs, OpenTelemetry metrics/traces |

## Data model (sketch)

```
users            id, email, email_verified_at, name, totp_secret (encrypted), created_at
passkeys         id, user_id, credential_id, public_key, sign_count, aaguid, name,
                 created_at, last_used_at
oauth_accounts   id, user_id, provider (google|discord|github), subject, email,
                 email_verified, created_at
email_codes      id, email, code_hash, link_token_hash, purpose, attempts,
                 expires_at, used_at
recovery_codes   id, user_id, code_hash, used_at
sessions         id, user_id, token_hash, created_at, last_seen_at, expires_at,
                 reauth_at, ip, user_agent, revoked_at
orgs             id, name, slug, created_at
org_members      org_id, user_id, role (owner|admin|member)
nodes            id, org_id, name, short_id, public_key, public_ipv4, public_ipv6,
                 status, key_revoked_at, sftp_enabled,
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

Built into the Panel, **passwordless**. There are no passwords to store, leak, or reset, and no auth vendor. Any session can send commands to nodes where Wings runs as root, so auth is treated as the most sensitive code in the Panel.

**Ways to sign in**

| Method | Details |
|---|---|
| **Passkeys** (preferred) | WebAuthn. Several per account (phone, laptop, security key). Phishing-resistant, and already two factors (device + biometric/PIN), so they skip the 2FA step. After a user's first sign-in, the Panel prompts them to add one. |
| **OAuth** | Google (OpenID Connect), Discord, GitHub. |
| **Email code or link** | One email with a **6-digit code and a sign-in link**; either works. Codes work across devices (read on the phone, type on the PC), and links can be used up by email scanners that open links automatically, so both are sent. 10-minute expiry, single use, 5 attempts. Also used to sign up and to verify the address. |

**Two-factor authentication**
- **TOTP** (authenticator apps) with **one-time recovery codes** given at setup.
- Required after **email and OAuth** sign-ins when enabled. Those are only as strong as the user's inbox or Google/Discord/GitHub account. Passkey sign-ins skip it, since they're already two factors.
- The Panel encourages every account that owns nodes to have a passkey or TOTP.

**Accounts**
- Users are keyed by our own ID; email is unique. An OAuth login is **linked to an existing account only if the provider says the email is verified**, otherwise someone could create an OAuth account with a victim's unverified email and take over their Raptor account. Discord and GitHub report whether the email is verified; unverified ones are treated as a new, separate identity.
- Users can add and remove passkeys, OAuth logins, and TOTP, but never remove their last way to sign in.
- **Recovery:** recovery codes (if TOTP is on), or an email code. Someone who controls the inbox can get in unless 2FA or passkeys-only is set, which is the honest limit of any passwordless system. For people who lose everything, there's a support process with identity checks.

**Sessions**
- The Panel issues its own sessions: random tokens (stored hashed) in a host-only `__Host-` cookie on `api.raptorpanel.net` (`HttpOnly`, `Secure`, `SameSite=Strict`, no `Domain`). The short-lived OAuth state cookie is `SameSite=Lax` so it survives the provider's redirect back.
- **CSRF and sibling subdomains:** every `*.raptorpanel.net` site is same-site, so `SameSite` alone doesn't stop the landing page or docs from sending credentialed requests. The API only accepts browser requests whose `Origin` is `https://app.raptorpanel.net` (CORS allows only that origin) and requires the Connect content type. The `__Host-` prefix stops sibling subdomains from setting or overwriting the session cookie.
- **Passkeys use the RP ID `app.raptorpanel.net`**, not `raptorpanel.net`, so no other subdomain can ask for signatures from them. Passkeys are bound to their RP ID permanently.
- Sessions expire after 30 days of inactivity and 90 days at most. Users see their devices and can log out one or all of them. Signing in rotates the session token.
- **Actions on nodes that destroy data, change code, or change access** (deleting servers, wiping reinstalls, changing eggs/images/startup, granting support access, adding SSH keys or sub-users, removing nodes) are **signed by the user's passkey and verified by Wings itself**, so the Panel can't forge them ([SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands)). They require a passkey.
- **Re-authentication for sensitive account actions** that stay in the Panel (billing changes, adding a node, adding or removing passkeys/OAuth/TOTP, changing the email): a passkey or TOTP (or an email code if neither is set up) within the last 5 minutes.

**Abuse protection**
- Rate limits per IP, per email address, and per account on sending codes and on every verification step.
- Cloudflare Turnstile on "email me a code", so the Panel can't be used to spam inboxes or run up the email bill.
- Every sign-in, failure, and change to sign-in methods goes to the audit log, and security changes (new passkey, TOTP disabled, new device) are emailed to the user.

**Staff** accounts are separate from customer accounts and must use hardware security keys (passkeys on a physical key).

## Permissions

- Org roles: `owner`, `admin`, `member`.
- Per-server grants for sub-users (e.g. `console.read`, `console.write`, `power`, `files.read`, `files.write`, `backups`, `schedules`, `startup`, `sftp`, …).
- When a user acts on a node, the Panel sends a **signed, short-lived grant** (≤ 5 min, bound to user + server + action) with the command. Wings verifies the signature and expiry locally.
- **Dangerous actions** additionally need the user's passkey signature over the exact command. Owners' keys are trusted by the node directly; sub-users need a **delegation signed by an owner's passkey** for each dangerous action they're allowed. The Panel stores and displays delegations but can't create them.

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
2. The owner gets an email + Panel notice and approves (may lower level/duration) or denies. **Approval is signed with the owner's passkey.**
3. Wings only accepts the support grant with the owner's signature, enforces it like any grant, and checks expiry locally. A compromised Panel or staff account can't grant itself access.
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
- Hostnames are listed in [ARCHITECTURE.md](ARCHITECTURE.md#hostnames). `api.raptorpanel.net` is behind the **Cloudflare proxy** (browsers and node connections alike); the origin only accepts traffic from Cloudflare (Authenticated Origin Pulls or an IP allowlist). Caddy terminates TLS at the origin.
- The web app, landing page, and docs are static sites on separate hosting from the Panel servers; the web app's deploy credentials are separate from everything else.
- `raptornodes.net` is **DNS-only**, on a plan sized for the record count, and on the Public Suffix List (see [ARCHITECTURE.md](ARCHITECTURE.md#node-dns)). Nothing of ours is hosted on it.
- The status page (`status.raptorpanel.net`) is hosted with a different provider than the Panel, and its DNS doesn't depend on Cloudflare.
- **Account security:** the registrar and Cloudflare accounts use hardware-key 2FA with no SMS recovery, the domains have registrar lock, and API tokens are scoped (the web app's deploy token can only deploy it; the Panel servers hold no Cloudflare token). Whoever controls DNS or the static host controls the code users run.
