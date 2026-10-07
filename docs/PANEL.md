# Panel

The Panel is Raptor's SaaS control plane: the web app at `app.raptorpanel.net` and the API at `api.raptorpanel.net`. It is **SaaS-only**. The stack is chosen for running it ourselves; there are no concessions or docs for self-hosting.

## Responsibilities

The Panel **owns**: users, orgs, members, roles, auth sessions, permission grants, billing, node identity (public keys) and node DNS, support access grants, the audit log, and the template (egg) catalog.

The Panel **mirrors** (read-only, disposable): servers, schedules, backup index, status, job summaries, and metric summaries, all reported by Wings.

The Panel **never stores**: game files, backup contents, live console, raw logs.

## Process model

One Go binary, one role (see [ARCHITECTURE.md](ARCHITECTURE.md#panel)):
- `panel serve api`: Connect API, WebSockets for browsers and nodes, River jobs. Several instances can run; requests for a node held by another instance are forwarded through Postgres `LISTEN/NOTIFY`.

Configured by environment (`panel` with no arguments lists them all):
- `PANEL_DATABASE_URL`, `PANEL_API_ADDR`.
- `PANEL_SIGNING_KEY`: the file with the Panel's Ed25519 signing key, kept apart from other secrets; nodes pin its public key.
- `PANEL_DATA_KEY`: the file whose key encrypts TOTP secrets; without it, two-factor authentication is off. Back it up like the signing key.
- `PANEL_APP_URL` (the web app's origin, the only one browsers may call from, and the passkey RP ID) and `PANEL_API_URL` (for OAuth callbacks).
- `PANEL_{GOOGLE,GITHUB,DISCORD}_CLIENT_ID` and `_CLIENT_SECRET`, `PANEL_TURNSTILE_SECRET`, `PANEL_CLIENT_IP_HEADER`, `PANEL_NODE_DOMAIN`, `PANEL_CLOUDFLARE_DNS_TOKEN`, `PANEL_CLOUDFLARE_ZONE_ID`.
- `PANEL_RESEND_API_KEY`, `PANEL_MAIL_FROM` (as `Raptor <account@mail.raptorpanel.net>`; not `no-reply`, which costs deliverability and trust), and `PANEL_MAIL_REPLY_TO` (a mailbox someone reads, as `support@raptorpanel.net`, so "this wasn't me" replies reach a person): email through Resend from `mail.raptorpanel.net`, with click and open tracking off (they'd rewrite sign-in links and add pixels to security mail) and TLS enforced. `PANEL_MAIL_LOG=1`, development only, sends emails (codes included) to the log instead, and to `PANEL_MAIL_LOG_FILE` as plain text if set (`task dev:mail` shows them); setting both is an error, and with neither, email sign-in and invitations are off.

Admin commands on the same binary: `panel migrate`, `panel keygen <path>`, and, for development and operators, `panel org create <name>` (an org with no members) and `panel join-token <org-id>`; users make orgs and join tokens through the API (`OrgService`). `panel rollout start <version>` (and `status`, `pause`, `resume`, `cancel`) runs a staged Wings update ([WINGS.md](WINGS.md#updates)).

## Stack

| Concern | Choice |
|---|---|
| Language | Go |
| API | Connect-RPC (protobuf), serves both Connect/gRPC and JSON |
| Database | Postgres, `pgx` + `sqlc` |
| Jobs | River (Postgres-backed) |
| Pub/sub | Postgres `LISTEN/NOTIFY` |
| Auth | Built into the Panel, passwordless: passkeys (`go-webauthn/webauthn`), OAuth (`golang.org/x/oauth2`, `coreos/go-oidc`), email codes, TOTP (`pquerna/otp`) |
| Email | **Resend** (`resend-go`), from its own sending subdomain: sign-in codes, invitations, security notices |
| Billing | Polar |
| Object storage | S3-compatible object storage (hosted backups, doctor bundles) |
| Frontend | React, TypeScript, Vite, TanStack Router + Query, shadcn/ui, Tailwind, xterm.js, Monaco |
| Observability | `slog` structured logs, OpenTelemetry metrics/traces |

## Data model (sketch)

```
users            id, email, email_verified_at, name, webauthn_handle, totp_secret (encrypted),
                 totp_enabled_at, totp_last_step, created_at
totp_setups      user_id, secret (encrypted), expires_at
pending_signins  id, user_id, token_hash, attempts, expires_at
passkeys         id, user_id, credential_id, credential (public key, counter, flags,
                 transports), name, created_at, last_used_at
webauthn_ceremonies  id, purpose (register|signin|reauth), session_id, data, expires_at
oauth_accounts   id, user_id, provider (google|discord|github), subject, email,
                 email_verified, created_at, last_used_at
oauth_flows      id, state_hash, provider, verifier, nonce, session_id, expires_at
email_codes      id, email, code_hash, link_token_hash, purpose, attempts,
                 expires_at, used_at
recovery_codes   id, user_id, code_hash, used_at, created_at
sessions         id, user_id, token_hash, created_at, last_seen_at, expires_at,
                 reauth_at, ip, user_agent, revoked_at
orgs             id, name, created_at
org_members      org_id, user_id, role (owner|admin|member), created_at
org_invitations  id, org_id, email, role, token_hash, invited_by, expires_at,
                 accepted_at, revoked_at
nodes            id, org_id, name, short_id, public_key, public_ipv4, public_ipv6,
                 status, key_revoked_at, sftp_enabled,
                 wings_version, protocol_version, last_seen_at, last_acked_seq,
                 support_access_disabled, billing_state
join_tokens      id, org_id, token_hash, expires_at, used_at
server_grants    org_id, user_id, node_id, server_id, permissions[], granted_by, updated_at
support_grants   id, node_id, staff_id, level, reason, ticket_ref,
                 approved_by, expires_at, revoked_at
ssh_keys         id, user_id, name, public_key, fingerprint, created_at, last_used_at
                 (users.sftp_username: the SFTP login's first part)
audit_log        id, org_id, user_id, actor (user|staff|system), actor_id, action, target,
                 ip, user_agent, metadata, at

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

How email sign-in works (`internal/panel/auth`): the code and link are one row, stored hashed (the code bound to that row), and either uses it up. The link's token is after `#` (`/signin/link#…`), so it never reaches a server log; the web app reads it and sends it to `FinishEmailSignIn`. Wrong codes count against the row's 5 attempts, and an address takes at most 20 wrong codes a day, whichever IPs they come from (otherwise 5 codes an hour × 5 tries is 600 guesses a day at one account). Sending is limited to 5 emails an hour per address and 20 per IP, and checking to 50 tries an hour per IP; the counts are in Postgres, so every instance shares them. The answer to "email me a code" is the same whether or not the address has an account. Emails go through Resend (`auth.Resend`): plain text, an idempotency key per email, and one retry with the same key after a rate limit or network error, so a send whose answer was lost isn't delivered twice. Without Resend configured, they go nowhere (the API says email isn't set up), except in development, where `PANEL_MAIL_LOG=1` writes them (codes and all) to the log.

How passkeys work (`internal/panel/auth`, `go-webauthn/webauthn`): every passkey must be discoverable and verify the user (fingerprint, face, or PIN), so signing in needs no email and a passkey counts as two factors. Each ceremony's challenge is stored in `webauthn_ceremonies` and used once, even if the answer is wrong; registration and re-authentication ceremonies are bound to the session that started them. The user handle stored in passkeys is 32 random bytes, not the account's ID. A passkey whose counter goes backwards is refused as a possible copy (synced passkeys report 0 and are exempt). The RP ID and origin come from `PANEL_APP_URL`. Adding or removing a passkey emails the user.

How OAuth works (`internal/panel/auth`, `golang.org/x/oauth2`, `coreos/go-oidc`): the web app calls `BeginOAuth`, which stores the flow (PKCE verifier, OpenID nonce, and for linking the session that asked), sets the state in a `SameSite=Lax` `__Host-raptor_oauth` cookie for 10 minutes, and returns the provider's URL. The provider sends the browser back to `https://api.raptorpanel.net/oauth/<provider>/callback`, outside `/api` because it's a navigation with no `Origin`. There the state in the URL must match the cookie (no login CSRF), the flow is used up, and the code is exchanged with the PKCE verifier. Google's ID token is checked against Google's keys, this client ID, and the nonce; GitHub's primary email and Discord's email come from their APIs with their verified flags. Then the browser is redirected to the web app: `/` with a session, `/signin/second-factor` if the account has TOTP, `/settings/security?linked=<provider>` after linking, or `/signin?error=<code>`. Each provider is on once `PANEL_<PROVIDER>_CLIENT_ID` and `_CLIENT_SECRET` are set; the OAuth app's callback URL is `PANEL_API_URL/oauth/<provider>/callback`. A provider joining an existing account by its email, linking, and unlinking all email the user.

**The web app's side** (`web/src/routes/signin.*`, `web/src/lib`): `/signin` offers a passkey (usernameless), the providers `GetSignInMethods` lists, and an email code; `/signin/link` reads the emailed link's token from the fragment, clears it from the address bar, and sends it; `/signin/second-factor` takes an app or recovery code. Pages that need a session redirect to `/signin?next=…` and come back after (only to paths inside the app). `/settings/security` manages passkeys, the authenticator app (its QR code is drawn in the page by `uqr`, so the secret goes nowhere else), recovery codes, linked accounts, devices, and the account's activity. The org pages (`/`, `/orgs/<id>`, `/orgs/<id>/nodes/<id>`) list orgs, members, invitations, the org's log, its nodes (`ListNodes`, with whether each is connected), and each node's servers (`ListServers`, from the mirror). Members see only the nodes and servers they have access to, with their permissions on each; admins can tick what each member may do per server. `/invite#<token>` keeps the token in the tab's session storage while the user signs in, then accepts it. Sensitive changes go through `useReauth()`: when the API answers that the user must confirm it's them, a dialog does `BeginReauth`/`FinishReauth` with whatever the account has (passkey, app code, or emailed code) and the change is retried. The API is called with credentials included (`VITE_API_URL` in production, `/api` through Vite's proxy in development); the session cookie is `HttpOnly`, so the page never sees it. WebAuthn options and answers are converted to and from JSON by hand (`lib/webauthn.ts`), since not every browser has `parse*OptionsFromJSON` and `toJSON` yet.

**Two-factor authentication**
- **TOTP** (authenticator apps) with **one-time recovery codes** given at setup.
- Required after **email and OAuth** sign-ins when enabled. Those are only as strong as the user's inbox or Google/Discord/GitHub account. Passkey sign-ins skip it, since they're already two factors.
- The Panel encourages every account that owns nodes to have a passkey or TOTP.

How TOTP works (`internal/panel/auth`, `pquerna/otp`): 30-second, 6-digit, SHA-1 codes, which is what every authenticator app does, accepted one step either side for drifting clocks. The step a code used is stored, so no code works twice, even in another sign-in. The secret is encrypted with AES-256-GCM under `PANEL_DATA_KEY`, bound to the user's ID; without that key TOTP is off. It's turned on only after a code from the app checks out, which also gives 10 recovery codes (80 random bits each, stored hashed, shown once, single use). After an email sign-in to an account with TOTP, the pending sign-in sits in its own `__Host-raptor_signin` cookie for 10 minutes and 5 tries, and `FinishSecondFactor` takes an app code or a recovery code. Turning TOTP on or off, making new recovery codes, and using one all email the user.

**Accounts**
- Users are keyed by our own ID; email is unique. An OAuth login is **linked to an existing account only if the provider says the email is verified**, otherwise someone could create an OAuth account with a victim's unverified email and take over their Raptor account. Google, Discord, and GitHub all report whether the email is verified; a new login with an unverified one is refused ("verify your email with them first"), since an account needs a unique address and an unverified one proves nothing. Once linked (from account settings, after re-authenticating), a provider account signs in whatever its email says.
- Users can add and remove passkeys, OAuth logins, and TOTP, but never remove their last way to sign in.
- **Recovery:** recovery codes (if TOTP is on), or an email code. Someone who controls the inbox can get in unless 2FA or passkeys-only is set, which is the honest limit of any passwordless system. For people who lose everything, there's a support process with identity checks.

**Sessions**
- The Panel issues its own sessions: random tokens (stored hashed) in a host-only `__Host-` cookie on `api.raptorpanel.net` (`HttpOnly`, `Secure`, `SameSite=Strict`, no `Domain`). The short-lived OAuth state cookie is `SameSite=Lax` so it survives the provider's redirect back.
- **CSRF and sibling subdomains:** every `*.raptorpanel.net` site is same-site, so `SameSite` alone doesn't stop the landing page or docs from sending credentialed requests. The API only accepts browser requests whose `Origin` is `https://app.raptorpanel.net` (CORS allows only that origin) and requires the Connect content type. The `__Host-` prefix stops sibling subdomains from setting or overwriting the session cookie.
- **Passkeys use the RP ID `app.raptorpanel.net`**, not `raptorpanel.net`, so no other subdomain can ask for signatures from them. Passkeys are bound to their RP ID permanently.
- Sessions expire after 30 days of inactivity and 90 days at most. Users see their devices and can log out one or all of them. Signing in rotates the session token, and ends the session the browser had before.
- **Actions on nodes that destroy data, change code, or change access** (deleting servers, wiping reinstalls, changing eggs/images/startup, granting support access, adding SSH keys or sub-users, removing nodes) are **signed by the user's passkey and verified by Wings itself**, so the Panel can't forge them ([SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands)). They require a passkey.
- **Re-authentication for sensitive account actions** that stay in the Panel (billing changes, adding a node, adding or removing passkeys/OAuth/TOTP, changing the email): a passkey or TOTP (or an email code if neither is set up) within the last 5 minutes. Signing in with a passkey counts; signing in by email counts only on accounts with no passkey or TOTP, so someone who gets into the inbox can't sign in and remove the passkeys. The API answers `FAILED_PRECONDITION` when one is needed, and the web app runs `BeginReauth`/`FinishReauth` and retries.

**Abuse protection**
- Rate limits per IP, per email address, and per account on sending codes and on every verification step.
- Cloudflare Turnstile on "email me a code", so the Panel can't be used to spam inboxes or run up the email bill. Its script never runs on the app's origin: the form embeds `verify.raptorpanel.net` (`web/verify`, a separate static page) in an iframe with no permissions, which passes the token back by `postMessage` to `https://app.raptorpanel.net` only, and the Panel checks the token was solved on that hostname (`PANEL_TURNSTILE_HOSTNAME`). `task dev` uses Cloudflare's always-pass test keys, with the page on `localhost:5174`. Production's site key is in `web/.env.production` (public, like everything in the page); the secret is only in the Panel's environment.
- Every sign-in, failure, and change to sign-in methods goes to the audit log, and security changes (new passkey, TOTP disabled, new device) are emailed to the user.

**Staff** accounts are separate from customer accounts and must use hardware security keys (passkeys on a physical key).

## Permissions

- Org roles (`internal/panel/orgs`, `OrgService`):
  - **Owners** can do everything, and only they change roles. An org always keeps an owner; the check locks the owners' rows, so two owners stepping down at once still leave one.
  - **Admins** rename the org, invite admins and members, remove members, and make join tokens (with a recent re-authentication, since a node runs as root).
  - **Members** see the org and its members; what they can do on servers comes from their grants.
  - Anyone can leave, except the last owner. Orgs someone isn't in are `NOT_FOUND` to them, so IDs reveal nothing.
- **Row-level security** (migration `00013_rls.sql`) backs these checks up in Postgres. Requests made for a user (`orgs.Service.asUser`) run as the role `raptor_app` with `raptor.user_id` set for the transaction. The policies then show only that user's orgs, their members, the users they share an org with, and those orgs' nodes and mirror. Only admins and owners can rename an org or manage invitations, only owners can change roles, and members can only remove themselves. `raptor_app` can't add members or see join tokens at all; creating an org and accepting an invitation run as the Panel, with their own checks. The Panel's own work (node connections, the mirror, rollouts, signing in) runs as the tables' owner, which RLS doesn't apply to. This guards against a handler that forgets a check, not against SQL injection, which could reset the role. The migration creates `raptor_app`, so the Panel's database user needs `CREATEROLE` the first time.
- **Invitations** are emailed with a link (`/invite#<token>`, the token hashed in the database), last 7 days, and work only for someone signed in with the invited address. Accepting when already a member keeps the higher role. Each org can send 50 a day and have 100 pending.
- **Per-server grants for members** (`server_grants`, `OrgService.SetServerAccess`): `console.write`, `power`, `files.read`, `files.write`, `backups`, `schedules`, `startup`, `reinstall`, `sftp`. Admins and owners set them and can always do everything themselves. Removing someone from the org removes their grants. `internal/panel/perms` maps every Wings action to the permission it needs, or to admins and owners only (creating and deleting servers, backup policy and destinations, node settings and updates, keys). An action that isn't listed is refused, and a test fails if Wings has an action the list doesn't cover.
- **Sending commands** (`CommandService.Execute`): the Panel checks the user may run the action under row-level security. A member without a grant on that server gets `NOT_FOUND`, the same as an outsider. The Panel then signs a grant for exactly that command (user, node, server, action, command ID, expiry) and delivers it to the node. For passkey-signed actions, the browser picks the command ID (UUIDv7) and expiry (at most 10 minutes away), since they're part of what the passkey signs. Commands that change something go in the org's audit log as `command`, with whether they were signed and how they ended; reads (`files.list`, `files.read`, ...) don't. Each user can send 300 commands a minute.
- When a user acts on a node, the Panel sends a **signed, short-lived grant** (≤ 5 min, bound to user + server + action) with the command. Wings verifies the signature and expiry locally.
- **Dangerous actions** additionally need the user's passkey signature over the exact command. Owners' keys are trusted by the node directly; sub-users need a **delegation signed by an owner's passkey** for each dangerous action they're allowed. The Panel stores and displays delegations but can't create them.

## Audit log

One table (`audit_log`) for account and org events, with the IP address and browser of the request, kept a year. Users see their account's events (`AuthService.ListActivity`); admins and owners see their org's (`OrgService.ListAuditLog`), 50 at a time, newest first.

- **Account:** `signin` (method: `email`, `passkey`, `totp`, `recovery_code`, `google`, `github`, `discord`; whether it's a new device), `signin.first_factor` (waiting for TOTP), `signin.failed` (a wrong code, a refused passkey, an unverified provider email), `reauth`, `reauth.failed`, `session.signout`, `session.revoke`, `passkey.add|remove|rename`, `totp.enable|disable`, `recovery_codes.regenerate`, `oauth.link|unlink`.
- **Org:** `org.create|rename`, `member.role|remove|leave`, `invitation.create|revoke|accept`, `join_token.create`.

Sign-ins and org changes are written in the same transaction as the change, so neither happens without the other. Org events are written under row-level security as the user (they can add their own org's events, never change or delete any), and a former member's address isn't shown on their old events once nobody shares an org with them. A sign-in from a browser (by user agent) the account hasn't used before emails the user.

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

- A primary server, deployed with Docker Compose: Caddy and two Panel instances, so deploys replace one at a time (#200). The hosting provider is an operational choice and is intentionally not fixed in these docs. The files are in `deploy/`, and [DEPLOY.md](DEPLOY.md) is the step-by-step guide.
- Postgres with WAL archiving via pgBackRest to **object storage in a different location**, plus a **streaming replica on a second server** at launch (see [RELIABILITY.md](RELIABILITY.md)).
- Hostnames are listed in [ARCHITECTURE.md](ARCHITECTURE.md#hostnames). `api.raptorpanel.net` is behind the **Cloudflare proxy** (browsers and node connections alike); the origin only accepts traffic from Cloudflare (per-hostname Authenticated Origin Pulls with our own certificate, and a firewall admitting Cloudflare's addresses; #201). Caddy terminates TLS at the origin.
- The web app, landing page, and docs are static sites on separate hosting from the Panel servers (Cloudflare Pages; #203); the web app's deploy credentials are separate from everything else. The app and the Turnstile page send strict Content-Security-Policy headers (#206).
- Secrets are in 1Password only, written to memory at boot by a read-only service account (#202).
- `raptornodes.net` is **DNS-only**, on a plan sized for the record count, and on the Public Suffix List (see [ARCHITECTURE.md](ARCHITECTURE.md#node-dns)). Nothing of ours is hosted on it.
- The status page (`status.raptorpanel.net`) is hosted with a different provider than the Panel, and its DNS doesn't depend on Cloudflare.
- **Account security:** the registrar and Cloudflare accounts use hardware-key 2FA with no SMS recovery, the domains have registrar lock, and API tokens are scoped (the web app's deploy token can only deploy Pages; the Panel servers hold one Cloudflare token, which can only edit `raptornodes.net`'s DNS records, from the servers' addresses). Whoever controls DNS or the static host controls the code users run.
