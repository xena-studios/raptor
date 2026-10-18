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
- `PANEL_DATABASE_URL`, `PANEL_API_ADDR`. Each instance's pool has at least 16 connections unless the URL sets `pool_max_conns`: its leader loops each hold one for their lock while they use another, so a pool the size of a small server's CPU count can deadlock.
- `PANEL_SIGNING_KEY`: the file with the Panel's Ed25519 signing key, kept apart from other secrets; nodes pin its public key.
- `PANEL_DATA_KEY`: the file whose key encrypts TOTP secrets and the copies of nodes' backup keys; without it, two-factor authentication is off and no backup keys are kept. Back it up like the signing key.
- `PANEL_APP_URL` (the web app's origin, the only one browsers may call from, and the passkey RP ID) and `PANEL_API_URL` (for OAuth callbacks).
- `PANEL_{GOOGLE,GITHUB,DISCORD}_CLIENT_ID` and `_CLIENT_SECRET`, `PANEL_TURNSTILE_SECRET`, `PANEL_CLIENT_IP_HEADER`, `PANEL_NODE_DOMAIN`, `PANEL_CLOUDFLARE_DNS_TOKEN`, `PANEL_CLOUDFLARE_ZONE_ID`.
- `PANEL_RESEND_API_KEY`, `PANEL_MAIL_FROM` (as `Raptor <account@mail.raptorpanel.net>`; not `no-reply`, which costs deliverability and trust), and `PANEL_MAIL_REPLY_TO` (a mailbox someone reads, as `support@raptorpanel.net`, so "this wasn't me" replies reach a person): email through Resend from `mail.raptorpanel.net`, with click and open tracking off (they'd rewrite sign-in links and add pixels to security mail) and TLS enforced. `PANEL_MAIL_LOG=1`, development only, sends emails (codes included) to the log instead, and to `PANEL_MAIL_LOG_FILE` as plain text if set (`task dev:mail` shows them); setting both is an error, and with neither, email sign-in and invitations are off.

- `PANEL_TELEGRAM_BOT_TOKEN` and `PANEL_TELEGRAM_WEBHOOK_SECRET` (32 or more random characters): Raptor's Telegram bot for alerts; without them, Telegram channels aren't offered. `PANEL_ALERTS_ALLOW_PRIVATE=1`, development only, lets alert channels reach private addresses.

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
| Frontend | React, TypeScript, Vite, TanStack Router + Query, shadcn/ui, Tailwind, xterm.js, Monaco; the look is [DESIGN.md](DESIGN.md) |
| Observability | `slog` structured logs, OpenTelemetry metrics/traces over OTLP to Grafana Cloud (#209) |

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
                 status, key_revoked_at, sftp_enabled, sftp_port, sftp_host_key,
                 wings_version, protocol_version, last_seen_at, last_acked_seq,
                 support_access_disabled, billing_state
join_tokens      id, org_id, token_hash, expires_at, used_at
server_grants    org_id, user_id, node_id, server_id, permissions[], granted_by, updated_at
support_grants   id, node_id, staff_id, level, reason, ticket_ref,
                 approved_by, expires_at, revoked_at
sftp_passwords   id, user_id, node_id, server_id, username (t-…), secret_hash, expires_at
                 (temporary SFTP passwords, one per user and server; DECISIONS #222)
audit_log        id, org_id, user_id, actor (user|staff|system), actor_id, action, target,
                 ip, user_agent, metadata, at

-- mirror (rebuilt from Wings events)
m_servers        node_id, server_id, name, egg_ref, status, config jsonb, version
m_schedules      node_id, schedule_id, server_id, definition jsonb
m_backups        node_id, backup_id, server_id, size, created_at, destination
m_jobs           node_id, job_id, server_id, type, status, started_at, finished_at
schedule_runs    id, node_id, server_id, schedule_id, run_key, status, reason, steps jsonb,
                 started_at, finished_at (history from events, not rebuilt)

-- billing
billing_accounts    org_id, subscription_id, status, seats, current_period_end, trial_end,
                    cancel_at_period_end, past_due_at, trial_used, unpaid_since, unpaid_notified,
                    nodes, auto_canceled, fetched_at
billing_ledger      id, org_id, kind, quantity, amount_cents, currency, polar_ref, data, at
billing_webhooks    id, type, received_at

-- alerts
alert_channels      id, org_id | user_id, type, name, config, secret (sealed), hint, failing_since,
                    last_error, last_sent_at, disabled_at
alert_rules         id, org_id, name, condition, node_id, server_id, threshold, for_minutes,
                    channel_ids, notify_roles, notify_user_ids, notify_resolved, enabled
alert_deliveries    id, org_id, rule_id, channel_id, label, message, status, attempts, next_at, error
alert_notifications id, user_id, org_id, rule_id, message, created_at, seen_at, dismissed_at

-- node DNS
dns_retired         name, type     (records to delete: old n- hostnames, player subdomains)
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

How OAuth works (`internal/panel/auth`, `golang.org/x/oauth2`, `coreos/go-oidc`): the web app calls `BeginOAuth`, which stores the flow (PKCE verifier, OpenID nonce, and for linking the session that asked), sets the state in a `SameSite=Lax` `__Host-raptor_oauth` cookie for 10 minutes, and returns the provider's URL. The provider sends the browser back to `https://api.raptorpanel.net/oauth/<provider>/callback`, outside `/api` because it's a navigation with no `Origin`. There the state in the URL must match the cookie (no login CSRF), the flow is used up, and the code is exchanged with the PKCE verifier. Google's ID token is checked against Google's keys, this client ID, and the nonce; GitHub's primary email and Discord's email come from their APIs with their verified flags. Then the browser is redirected to the web app: `/` with a session, `/signin/second-factor` if the account has TOTP, `/settings?linked=<provider>` after linking, or `/signin?error=<code>`. Each provider is on once `PANEL_<PROVIDER>_CLIENT_ID` and `_CLIENT_SECRET` are set; the OAuth app's callback URL is `PANEL_API_URL/oauth/<provider>/callback`. A provider joining an existing account by its email, linking, and unlinking all email the user.

**The web app's side** (`web/src/routes/signin.*`, `web/src/lib`): `/signin` offers a passkey (usernameless), the providers `GetSignInMethods` lists, and an email code; `/signin/link` reads the emailed link's token from the fragment, clears it from the address bar, and sends it; `/signin/second-factor` takes an app or recovery code. Pages that need a session redirect to `/signin?next=…` and come back after (only to paths inside the app). `/settings` is the account: General (the name; the email, moved by `StartEmailChange`/`FinishEmailChange` with a code sent to the new address after "confirm it's you", and the old address told; the theme, a mode (system, light, or dark) and a style (standard; high contrast, for low vision; or calm, neutral greys with the red kept for what's primary or dangerous), kept in `localStorage` so the page starts in it and on the account (`UpdateProfile`) so it follows the user to other browsers; linked accounts; and `DeleteAccount`), Security (passkeys, the authenticator app (its QR code is drawn in the page by `uqr`, so the secret goes nowhere else), recovery codes, SSH keys, and devices), and Activity. Deleting an account needs "confirm it's you" and is refused while it's the only owner of an org that has other members or nodes; an org it leaves empty keeps its rows (node rows never go) with its invitations revoked, the org's log keeps a line saying who left, and the account's own activity goes with it. The org pages (`/`, `/orgs/<id>`, `/orgs/<id>/nodes/<id>`) list orgs, members, invitations, the org's log, its nodes (`ListNodes`, with whether each is connected), and each node's servers (`ListServers`, from the mirror). Members see only the nodes and servers they have access to, with their permissions on each; admins can tick what each member may do per server. `/invite#<token>` keeps the token in the tab's session storage while the user signs in, then accepts it. Sensitive changes go through `useReauth()`: when the API answers that the user must confirm it's them, a dialog does `BeginReauth`/`FinishReauth` with whatever the account has (passkey, app code, or emailed code) and the change is retried. The API is called with credentials included (`VITE_API_URL` in production, `/api` through Vite's proxy in development); the session cookie is `HttpOnly`, so the page never sees it. WebAuthn options and answers are converted to and from JSON by hand (`lib/webauthn.ts`), since not every browser has `parse*OptionsFromJSON` and `toJSON` yet.

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
- **Actions on nodes that destroy data, change code, or change access** (deleting servers, wiping reinstalls, changing eggs/images/startup, granting support access, delegating signed actions to members) are **signed by the user's passkey and verified by Wings itself**, so the Panel can't forge them ([SECURITY-MODEL.md](SECURITY-MODEL.md#passkey-signed-commands)). They require a passkey.
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
  - Anyone can leave, except the last owner. Orgs someone isn't in are `NOT_FOUND` to them, so IDs reveal nothing. The org's Settings page offers members and admins **Leave organization** and owners **Delete organization** (owners who want to leave instead can from the Members page).
  - **Deleting an org** (`OrgService.DeleteOrg`, owners, with a recent re-authentication, typing its name in the app; #283): refused while it has nodes, since removing them is what lets go of their DNS names, backup storage, and billing seats (a plan with no nodes ends at the period's end on its own). Its unused join tokens are expired first (which waits for a node joining right then, so the node count after sees it), then its invitations revoked and every member taken out, and the row is marked `deleted_at` and kept with its audit log (`org.delete`). A suspended org can't be deleted by its owner.
- **Row-level security** (migration `00013_rls.sql`) backs these checks up in Postgres. Requests made for a user (`orgs.Service.asUser`) run as the role `raptor_app` with `raptor.user_id` set for the transaction. The policies then show only that user's orgs, their members, the users they share an org with, and those orgs' nodes and mirror. Only admins and owners can rename an org or manage invitations, only owners can change roles, and members can only remove themselves. `raptor_app` can't add members or see join tokens at all; creating an org and accepting an invitation run as the Panel, with their own checks. The Panel's own work (node connections, the mirror, rollouts, signing in) runs as the tables' owner, which RLS doesn't apply to. This guards against a handler that forgets a check, not against SQL injection, which could reset the role. The migration creates `raptor_app`, so the Panel's database user needs `CREATEROLE` the first time.
- **Invitations** are emailed with a link (`/invite#<token>`, the token hashed in the database), last 7 days, and work only for someone signed in with the invited address. Accepting when already a member keeps the higher role. Each org can send 50 a day and have 100 pending.
- **Per-server grants for members** (`server_grants`, `OrgService.SetServerAccess`): `console.read` (watching the console), `console.write` (sending commands, which includes watching), `power`, `files.read`, `files.write`, `backups`, `schedules`, `startup`, `reinstall`, `sftp`. Admins and owners set them and can always do everything themselves. Removing someone from the org removes their grants. `internal/panel/perms` maps every Wings action to the permission it needs, or to admins and owners only (creating and deleting servers, backup policy and destinations, node settings and updates, keys). An action that isn't listed is refused, and a test fails if Wings has an action the list doesn't cover.
- **A server's settings** (`OrgService.GetServer`): the mirrored config with the egg's images and variables, for the settings page; only to admins, owners, and members with `startup`, since variables can hold secrets. A node that comes back on a new Wings version gets a fresh mirror snapshot, so what a new version reports shows up at once.
- **Renaming and removing nodes** (`OrgService.RenameNode`, `RemoveNode`): admins and owners; removing needs a recent confirmation. A removed node's key is refused, its DNS records go, its mirror and members' grants are dropped, and its connection is closed wherever it is (a `drop` notification to the instance holding it). The machine's servers keep running; `raptor relink` with a join token brings the node back.
- **Eggs for new servers** come from the egg library ([below](#eggs)). `OrgService.ListNodes` includes each node's architecture, cores, and memory (from when it linked), and `ListServers` each server's ports and install state, so the web app can offer the eggs a node can run and a free port.
- **The live socket** (`/api/live`, `internal/panel/live`): one WebSocket per browser tab for consoles ([ARCHITECTURE.md](ARCHITECTURE.md#realtime-console-and-stats)). Each watch is checked like a command for `server.console` (`console.read`) and gets its own grant; it's reopened every 5 minutes to check again.
- **Sending commands** (`CommandService.Execute`): the Panel checks the user may run the action under row-level security. A member without a grant on that server gets `NOT_FOUND`, the same as an outsider. The Panel then signs a grant for exactly that command (user, node, server, action, command ID, expiry) and delivers it to the node. For passkey-signed actions, the browser picks the command ID (UUIDv7) and expiry (at most 10 minutes away), since they're part of what the passkey signs. Commands that change something go in the org's audit log as `command`, with whether they were signed and how they ended; reads (`files.list`, `files.read`, ...) don't. Each user can send 300 commands a minute. Params are at most 256 KB, except `files.write` (the editor saving a whole file, up to Wings' 4 MiB), which may be 6 MiB.
- When a user acts on a node, the Panel sends a **signed, short-lived grant** (≤ 5 min, bound to user + server + action) with the command. Wings verifies the signature and expiry locally.
- **Dangerous actions** additionally need the user's passkey signature over the exact command. Owners' keys are trusted by the node directly; sub-users need a **delegation signed by an owner's passkey** for each dangerous action they're allowed. The Panel stores and displays delegations but can't create them.

## Audit log

One table (`audit_log`) for account and org events, with the IP address and browser of the request, kept a year. Users see their account's events (`AuthService.ListActivity`); admins and owners see their org's (`OrgService.ListAuditLog`), 50 at a time, newest first.

- **Account:** `signin` (method: `email`, `passkey`, `totp`, `recovery_code`, `google`, `github`, `discord`; whether it's a new device), `signin.first_factor` (waiting for TOTP), `signin.failed` (a wrong code, a refused passkey, an unverified provider email), `reauth`, `reauth.failed`, `session.signout`, `session.revoke`, `passkey.add|remove|rename`, `totp.enable|disable`, `recovery_codes.regenerate`, `oauth.link|unlink`.
- **Org:** `org.create|rename`, `member.role|remove|leave`, `invitation.create|revoke|accept`, `join_token.create`, `egg.import|delete`.

**Where it's shown:** the account's Activity (Settings), the org's (org settings, and its newest few on the org's overview), a node's Activity tab (what was done to it and its servers: `ListAuditLog` with `node_id` alone), and a server's (`node_id` and `server_id`).

**Filters** (`ActivityFilter`, checked by `auth.ParseFilter`): words matched, case-insensitively and as typed (no wildcards), against an event's action, target, IP, browser, details, and the emails of who did it and who it was about; a date range (the browser's days: today, the last 7 or 30, or any two dates); an IP address or CIDR range (`ip <<= range`); who did it (org logs: a member's user ID); kinds of event, as actions or prefixes ending in `%` matched against the action or a command's own action (the app groups them: Power, Console, Files and SFTP, Backups, and so on); and failures only (`*.failed` actions and commands that failed). In the org's log, a node or server picked under "Where" narrows it the way their own tabs do. In the app, clicking an event's IP or person narrows the list to them; typing waits a moment before searching. Filters work with paging, since pages are still before an event ID.

Sign-ins and org changes are written in the same transaction as the change, so neither happens without the other. Org events are written under row-level security as the user (they can add their own org's events, never change or delete any), and a former member's address isn't shown on their old events once nobody shares an org with them. A sign-in from a browser (by user agent) the account hasn't used before emails the user.

## Backup destinations in the app

Destinations live on each node, credentials included: the Panel never stores them. They pass through it once, inside the signed `backup.destination.save` the browser sends, and neither the Panel's audit log nor the node's record of commands keeps command parameters.

- **A node's Backups tab** (admins): its destinations with their health (worked when, failing since when and why, what they store, the upload limit), a Test button, and adding or changing one: a folder on the node, S3-compatible (presets fill the endpoint for Backblaze B2, Cloudflare R2, Wasabi, AWS, Hetzner, OVH, and Google Cloud Storage), Azure, an SFTP server (the node fetches the host key, the owner compares the fingerprint and trusts it; sign in with the node's own key, shown to paste into `authorized_keys`, a password, or a private key), or WebDAV. Test works before saving; saving and removing take a passkey. Secrets are never shown back; left blank they're kept.
- **A server's Backups tab:** "Where backups go", each destination with what it keeps (Light, Standard, Long-term, or custom numbers) and what's left out; admins change it, with a passkey when it sends backups somewhere new off the node's own disk or keeps fewer (`web/src/lib/backup-destinations.ts` mirrors Wings' rule, to say so on the button). Each backup shows where it is, and "Back up now" can go to one place only. A schedule's backup step can too.
  - **Opening a backup** (Restore, on each row) shows it like the file manager, read-only, at `/backups?backup=<short ID>&path=…&file=…`: its folders in a tree and a list, and files opened in the editor to read (up to 4 MiB, as the editor; the egg's denied files are listed locked). A file or folder (or a selection, from the row's menu, a right-click, or the selection bar) is **restored to .restore**: copied into a new `.restore/<when the backup was taken>` folder beside the server's files, changing nothing else, with a note linking to it when it's done. **Restore everything**, top right, puts the whole server back as it was (every file replaced, files made since deleted; a safety backup first; passkey-signed) after one confirmation. Deleting a backup asks first, in a dialog.
- **Org settings → Backups** (admins): every connected node's destinations and their health in one place, linking to each node's tab.

## Raptor Backup Storage

Offsite backups with nothing to set up (`internal/panel/storage`), priced in [#230](DECISIONS.md): 10 GB included per paid node, then $12/TB-month by the GB.

- **One B2 bucket**, `raptor-backup-storage`, reached through its S3 API. Its name is pinned in Wings (`internal/shared/hosted`): B2 bucket names are unique across Backblaze, so a "Raptor storage" destination can only ever send backups to Raptor's bucket, whatever a Panel says.
- **Turning it on** for a node (`OrgService.EnableBackupStorage`, admins; the first, recommended choice in the node's Backups tab → Add destination, with nothing to fill in, #283): the Panel makes a B2 key that reaches only `orgs/<org>/nodes/<node>/` (list, read, and write files; no deleting for good, no other keys, no bucket settings), and sends the node a `backup.destination.save` with it as the destination `raptor`. Wings takes it unsigned because it can only be Raptor's bucket and that node's own folder (`backup.IsHosted`); pointing a server at it is unsigned for the same reason. The key's secret goes to the node and isn't kept by the Panel (`backup_storage` keeps its ID). Turning it on again makes a new key and deletes the old one. A node that's offline gets nothing and leaves no key behind.
- **A hacked node can't destroy its backups:** its key can't delete for good, so B2 turns its deletes (Kopia's cleanup included) into hiding the file, and the bucket keeps hidden versions 30 days. Backups wiped from a hacked node can be brought back within that time (in the app with PR 4: restoring the repository as it was before, from those versions, which Kopia can open at a point in time). Only the Panel's key deletes for good, 30 days after storage is turned off.
- **The data is the node's to read:** backups are encrypted by the node (Kopia, with the node's repository password) before they leave it, and the Panel doesn't have that password, so Raptor stores what it can't read. (Until the key copy of PR 4 exists, a dead node's backups can't be recovered elsewhere either.)
- **Turning it off:** the node removes the destination (signed, like any), then `DisableBackupStorage` deletes the key. The files stay 30 days, then the Panel deletes every version under the node's folder. Removing a node does the same.
- **Usage:** once a day, one Panel instance (an advisory lock) adds up each file's current version under each org's folder (`b2_list_file_versions`) into `backup_storage_usage`. Hidden versions are kept for the customer's protection, so they're Raptor's cost, not counted. `GetBackupStorage` shows it with what's included (10 GB × the org's nodes) and an estimate. Past 500 GB during beta, the org is asked to get in touch and it's logged; nothing is blocked. Billing reads the same table once billing exists.

## Backup keys

Each node encrypts its backups with its own key (WINGS.md#backups), so a node that dies takes the only copy with it unless one is kept elsewhere. By default the Panel keeps one (`internal/panel/backupkeys`, `backup_keys`):

- **Getting it:** when a node connects, and every 15 minutes for nodes without a copy, the Panel sends `backup.key` (one instance at a time, an advisory lock). In `panel` mode the node answers with the key; it's sealed with AES-256-GCM under `PANEL_DATA_KEY`, bound to the node's ID (a sealed key copied to another node's row doesn't open), and stored with its fingerprint. In `owner` mode the node answers with the mode, and the Panel deletes any copy.
- **Kept after a node is removed:** recovering a dead node's backups is what it's for. It opens only for that node's org (the recovery in PR 4b).
- **The node's Backups tab → Backup key:** who keeps it, the fingerprint, "Download the key" (an owner's passkey; or `raptor backup key` on the node, so it never passes through Raptor), and switching. Keeping it yourself asks to download it first and to confirm it's saved; it's unsigned, then `SyncBackupKey` drops the Panel's copy. Letting Raptor keep a copy takes an owner's passkey. `GetBackupKey` says whether the Panel has a copy, never the key.
- **What it means:** with a copy, Raptor could decrypt that node's backups (and for Raptor Backup Storage, it also has the storage), which the app says plainly. Owner mode is for those who'd rather it couldn't.

## Recovering backups

On a node's Backups tab, owners can **Recover backups** (WINGS.md#backups): `ListRecoverySources` lists the org's nodes, removed ones included, with whether the Panel has a copy of each one's key and whether it had Raptor Backup Storage whose files aren't deleted yet. Then:

- **From its Raptor Backup Storage:** `PrepareRecovery` (owners, audited as `backup.recover.prepare`) opens the Panel's copy of that node's key and makes a B2 key that only reads its folder, for 90 days; the browser signs and sends `backup.recover` with both.
- **From a destination of its own:** the destination form, plus that node's key (Raptor's copy filled in, or pasted from where the owner saved it).
- **From somewhere else:** the same, with a key the owner saved (another Panel, a machine never linked).
- **Look back in time** on an S3 or Raptor Backup Storage destination: its backups as they were at a time the owner picks, to bring back deleted ones.

Recovered backups of servers that aren't on the node show under **Recovered backups**, to restore onto one of its servers with an owner's passkey.

## Eggs

The egg library (`EggService`, `internal/panel/orgs/library.go`): what servers are made from. Shown together on an org's **Eggs** page:
- **Official:** Raptor's, managed by staff on the admin panel's **Eggs** page (`eggs.org_id` NULL). Orgs see the published ones. Raptor's catalog (`eggs/` in the repository) is seeded into them when the Panel starts ([EGGS.md](EGGS.md#in-the-official-library)); certified eggs are the files the conformance suite tests.
- **The org's own** (`eggs`): imported, made from scratch, or customized copies of any egg they can see. Admins and owners change them; members see them. Up to 100 per org (500 official).

Each egg has a status: a **draft** can't be deployed, a **published** one can, and an **archived** one is hidden and can't be deployed. Made-from-scratch eggs and copies start as drafts; imports are published. An egg's page shows what it runs: add-ons, runtimes, variables and who can change them, the startup command, ready lines and stop, the install script, the config files Wings edits, the file itself, and every version. From there: **Use egg** (the create-server wizard with it picked), **Customize** or **Duplicate** (a draft copy to edit, which remembers what it's based on), **Export**, and **Edit**.

**Editing** (`internal/eggs/spec`): the editor's tabs (Overview, Runtimes, Variables, Startup, Install, Config files, Add-ons) edit fields read from the egg file, and saving writes them back into it: only what the editor covers changes, keys it doesn't know stay, and the format's style is kept (Pterodactyl's JSON-encoded config strings and `a|b` rules, Pelican's objects and lists). The result is JSON, which both formats read, so an edited egg still imports into Pterodactyl or Pelican. Every catalog egg reads and writes back unchanged (`TestRoundTripCatalog`). The File tab edits the file itself. Variables have a kind (text, number, on/off, choice) that writes their Laravel rules; rules the kinds don't produce stay as written, as **Custom rules**.

Each save that changes the file is a new **version** (`egg_versions`, with who and an optional note). Saving on top of a version someone else replaced fails (`base_version`) rather than overwriting it. Servers keep the egg they were made from ([DECISIONS.md](DECISIONS.md) #45), so editing, archiving, or deleting an egg never changes a running server. An egg servers were made from can't be deleted, only archived (`egg_source` in the mirror counts them).

What a server gets: `GetEggFile` returns a published egg's file (any version), which the web app puts in the passkey-signed `server.create` as it is. The org's eggs and the official ones are read as the user, under row-level security; official eggs are changed on the Panel's own connection after the staff check (an admin with a passkey from the last 5 minutes), and go in the staff log. Org changes go in the org's audit log: `egg.create`, `egg.update`, `egg.customize`, `egg.publish`, `egg.unpublish`, `egg.archive`, `egg.delete`, `egg.import`.

### Community eggs

An org shares a published egg with everyone on Raptor (`ShareEgg`, `egg.share`; admins and owners, after a reminder that the whole file is public, with the org's name on it). The Eggs page's **Community** tab lists shared eggs (`ListCommunityEggs`: searched and filtered on the Panel, most added first or newest, 30 a page), with who shared each and how many orgs added it.

- **Adding** one (`CustomizeEgg` with `as_is`) copies it into the org, published and ready to deploy, after the same review as an import: images, the install script, the startup command, warnings, and a box saying the admin trusts it. Raptor doesn't check community eggs, and the app says so. The copy is the org's: later edits by the egg's org don't reach it.
- **Reporting** (`ReportEgg`): anyone in another org, once per egg until staff look, 10 an hour; harmful, broken, spam, or something else, with details. The egg's org isn't told who.
- **Moderation** (`ListEggReports`, `ModerateEgg`; the admin panel's Eggs page, Community and Reports tabs): staff admins take an egg out of the community with a reason (which closes its reports), put it back, or dismiss its reports. Orgs can't change `removed_at` (column privileges), and a removed egg can't be shared again by its org, which sees why on the egg's page and in its audit log (`egg.community.remove`, by Raptor staff). Copies other orgs already added stay theirs. Staff can also make an official copy of a community egg.

Unpublishing or archiving a shared egg takes it out of the community.

### Add-ons

`AddonService` (`internal/panel/orgs/addons.go`, `internal/panel/modrinth`, `internal/panel/hangar`) finds plugins, mods, and datapacks for servers whose egg turns on an add-on ([EGGS.md](EGGS.md#add-ons)): `SearchAddons` and `ListAddonVersions` (from Modrinth, or Hangar with `source: "hangar"`, plugins only), and `IdentifyAddons` (what each installed file is, by SHA-512 on Modrinth and then SHA-256 on Hangar, and the newest version for the server's loaders and game version). Any member of the org, 120 calls a minute each. The Panel asks both with its own user agent and keeps answers for 5 minutes (`internal/panel/webapi`). The web app's CSP allows images from `cdn.modrinth.com` and `hangarcdn.papermc.io` for project icons. `LookupWorkshopItems` names Steam Workshop items and collections for games whose egg takes them (`internal/panel/steam`, Steam's public API, no key; [EGGS.md](EGGS.md#steam-workshop)), at most 100 IDs a call, with previews only from `images.steamusercontent.com`.

### Importing

An egg comes in from a link or an upload: Pterodactyl or Pelican, JSON or YAML.

1. **Review** (`EggService.ReviewEggFile`, 30 an hour per user for links): the Panel fetches the link (`internal/panel/eggimport`): https on port 443 only, a GitHub file page read from its raw file, at most 3 redirects and all https, 1 MiB, 15 seconds, and only public addresses, checked on the address actually dialed so DNS can't point it inward. It parses the egg with the same parser Wings uses (refusing one that leaves parts to another egg with `config.extends` or `copy_script_from`) and returns what it would run: images and their registries, the install container, entrypoint, and script, the startup command, variables, and warnings: an image from a publisher the community eggs don't use (anything but Docker Hub's official images and the Pterodactyl, Pelican, and parkervcp ones), an install script that pipes a download into a shell, no CPUs named, or a claim to be certified. Nothing is saved.
2. The app shows all of it, and Import stays off until the admin ticks that they've looked and trust the egg.
3. **Import** (`ImportEggFile`): the reviewed file, sent back as it was, is parsed again and saved with its SHA-256 and link; the same file can't be imported twice into one library.

An imported egg is never certified, whatever it says.

## Schedule runs

The node fires schedules (see [WINGS.md](WINGS.md#scheduler)); the Panel keeps their history in `schedule_runs`, because Wings drops its events after a week. The mirror records a run from the node's events as it syncs: `schedule.run.queued` starts a run (keyed by its job ID), `schedule.run.finished` ends it with each step's result (or records it whole, if its start was never seen), and `schedule.run.skipped` is a run that didn't happen, with why (`missed`, `offline`, `still_running`). Each schedule keeps its last 100 runs; deleting a schedule or a server deletes its runs. A snapshot doesn't bring back runs from a gap in the events, so a run can be missing, never wrong.

`OrgService.ListScheduleRuns` (the `schedules` permission) lists a server's runs, or one schedule's, 25 at a time, newest first. A schedule's "last run" is the newer of the node's and the newest run here, so "Run now" counts. The server's Schedules tab shows them under the schedules, refreshed every 5 seconds: running (with the steps done), ran (with how long and how many steps), failed (at which step, and why), skipped (and why). A step's error is the node's words, so the app shows known ones as a sentence ("the server wasn't running") and anything else as "the backup failed" and the like.

## URLs

Orgs, nodes, and servers each have two IDs: the full one (a UUIDv7) and a **short ID**, its last part (`01a131b9-f6ae-7a48-9b53-4b5e5f2ce0f8` → `4b5e5f2ce0f8`), random in a UUIDv7 (#280). The app's URLs carry the short ones, and a server's has only its own (not its node's): `/orgs/d1c671ecf959/servers/4b5e5f2ce0f8/settings` (#285).

- The routes turn them back into full IDs before a page loads (`web/src/lib/ids.ts`): the org's from `ListOrgs`, a node's from its org's `ListNodes`, and a server's, with the node it's on, from `FindServer` (by full or short ID, in the org; a short ID two servers share, or one the user can't see, finds nothing), each looked up once and remembered. Pages read them with `useIds()` and only ever work with full IDs, so the API is unchanged.
- Links are built from full IDs as before; the routes' `params.stringify` shortens them.
- Old links still work: full IDs are redirected to short ones, and `/servers/<node>/<server>/…` to `/servers/<server>/…`. Alert emails link with short IDs.
- A node's or server's full ID is in the Details card on its Overview tab, like the account's and org's. A node's short ID is also its hostname, `<short ID>.raptornodes.net` (#279). `raptor ps` shows servers' short IDs, and Wings takes them, or the 8 characters it used to show, anywhere it asks for a server.

## Node addresses

A node's hostname (`<short id>.raptornodes.net`) resolves to the machine's IP address, so showing it is showing the IP. Many owners put a proxy in front of their servers (TCPShield and the like) and stream or share their screen, so the app treats both as private:
- The hostname and the public IPs are shown as dots until revealed, one at a time, and can be copied without being revealed: in the node and server lists, the node's header and details, a server's address, the Network tab, the connection test, and the SFTP dialog. Only admins and owners get the IPs from `ListNodes`; members never do.
- A server's **shown address** (`SetServerDisplayAddress`, admins and owners, on its Network tab, in the audit log) is what players connect to instead: a domain or IP, with a port or not. It's shown exactly as typed: the port is the owner's to add if players need it (a domain with an SRV record, or a proxy on 25565, needs none). Only the node's hostname, used when there's no shown address, gets the server's port. It's shown in the clear wherever the server's address is. It's a server's, not a node's: servers on one machine are often behind different proxies or domains, and the node's own pages (its header, details, and the node list) always show the node's hostname, hidden. A server without one shows its node's hostname, hidden. Migration 42 moved each node's old shown address onto its servers; `SetNodeDisplayAddress` now fails, and new servers start without one. The connection test still names the node's own hostname, hidden, because that's what it reaches.

## Hostname and time zone

`internal/panel/machines`. The Panel keeps each node's machine hostname and time zone (`nodes.hostname`, `nodes.timezone`): it reads them with `node.system` (as `panel:machine`) whenever the node connects, and again whenever an admin opens the System tab or changes the time zone there.

- **The hostname follows the node's name** by default: renaming the node renames the machine. The name is made into a hostname (lowercase, runs of anything but letters and digits become one hyphen, at most 63 characters; "Basement Box #2" is `basement-box-2`; a name with nothing left is its short ID, as in its DNS name). An admin can give the machine a hostname of its own on the System tab (`OrgService.SetNodeHostname`; `''` follows the name again), which then stays whatever the node is called. A hostname set straight through `node.hostname` counts as the admin's own too.
- A sync runs on one instance at a time (an advisory lock), when a node connects or is renamed and every minute: a connected node whose hostname isn't what it should be is sent `node.hostname`. A refusal is kept (`hostname_error`, shown on the System tab) and not sent again until what it should be changes. A hostname changed on the machine by hand is seen on the next connection and put back.
- **The node's time** is in the header of the node's pages and its servers' pages (servers run on the node's time zone, as `TZ`): the time there and its zone. Opening it shows that time in your own zone, UTC, and zones you add (kept in the browser, up to 8), with how far each is from the node, and converts a time you pick on the node to each of them. It isn't shown until the Panel has read the zone.

## Pop-outs

A server's console, file manager, and stats, and a node's stats (admins), each open in a window of their own from the button with the arrow out of a box (on the Console card, the Resources card, and the file manager's toolbar): no sidebar, just the server or node, its state, its power buttons, and a link back to it in the app. The pages are `/popout/<org>/servers/<node>/<server>/{console,files,stats}` and `/popout/<org>/nodes/<node>/stats` (`web/src/routes/popout.*`), with the same short IDs and permissions as the app's own pages. Each pop-out is one window by name: opening it again brings it to the front. A middle click or Ctrl/Cmd-click opens it in a tab instead (#282).

## File manager right-click

Right-clicking in the file manager opens a menu as a desktop file manager does (`ListMenu` in `web/src/components/file-manager/index.tsx`), in the list and in the folder tree alike: on a file, Open, Download, Rename, Make a copy, Compress or Extract here, Copy path, Delete; on a folder, the same plus New file and New folder inside it; on several selected items, Compress and Delete for all of them; on empty space, New file, New folder, Upload files, Copy folder path, Refresh. Right-clicking never changes the selection: the item the menu is for is outlined while it's open, and right-clicking inside a selection acts on the whole selection (#283). The `...` button on each row stays, for touch and keyboards.

## File manager trash

Deleting in the file manager always moves to the server's trash ([WINGS.md](WINGS.md#trash)), after one warning that says it can be put back for 7 days and still counts toward the disk limit; there's no "delete permanently" (#286). The trash is a place in the file manager, at the bottom of the folder sidebar with how many items it holds (and a button in the toolbar on small screens, where the sidebar is hidden); opening it shows its items in the main pane, each with Restore and Purge, and Empty trash purges them all. Purging is the only way anything is deleted for good, so it asks first (one item, or all of them and how much space that frees). It still counts toward the server's disk limit until it's purged (#284).

## Toasts

Every change made in the app is confirmed in a toast in the corner, gone after four seconds: renaming an org or a node, saving your profile, inviting someone, saving a schedule or a backup plan, starting a server, and the rest. An interceptor on the API transport (`web/src/lib/action-toasts.ts`) raises it once the Panel, or the node for a command, says it's done, with what it says listed per call in `web/src/lib/action-messages.ts` (and per Wings action for `CommandService.Execute`). Calls that aren't listed stay quiet: reads, sign-in steps, redirects (checkout), and calls whose result the page shows itself (a connection test, a join token).

The file manager says what it did itself, with the file names: created, renamed, copied, compressed or extracted, uploaded, moved to the trash, restored, purged, a path copied, and a file saved from the editor (not auto-saves). Failures stay in view on the page instead of a toast.

Toasts are Base UI's (`web/src/components/ui/toast.tsx`, no other library), with one global manager, so code outside components can raise one; errors are announced to screen readers right away.

## Code editor

Monaco (the file manager and egg scripts) is drawn in the app's colors, not a theme of its own: the text and gutter on the card, and the parts Monaco draws itself (suggestions, hovers, the find box, the right-click menu, the command palette) on the popover's colors with the app's accent, focus ring, corners, and type (`raptorTheme` in `web/src/components/monaco.tsx`). It follows the app's theme as it changes, the OS's included. There's no separate editor theme to pick (#281).

## Charts

Every chart in a titled graph (a node's and a server's resources, the staff overview) has **Save as a picture** and **Copy as a picture** (where the browser can put images on the clipboard). The picture is drawn from the chart's data on a canvas, not screenshotted, at 2400×1350 in the page's theme, with what it shows written on it, so it makes sense on its own in a support thread or a Discord channel. It's laid out like the app (the Console in [DESIGN.md](DESIGN.md)): the card's colour edge to edge, no boxes, sections divided by hairlines, data in mono. It has Raptor's logo (the running raptor and wordmark, drawn from the same paths as the app's, `web/src/lib/brand.ts`), the chart's title and note, what it's about (the node, or the server and its node), the time range and the time zone it's in, a readout row per series with its latest, low, average, and peak over the range, the chart (2px lines with a soft fill, round axis steps, the latest value labelled at each line's end, and the limit as a named dashed line), and when it was saved. Files are named like `raptor-smp-on-basement-box-cpu-2026-10-16-1405.png`. Nothing is sent anywhere (`web/src/lib/chart-image.ts`).

## Alerts

`internal/panel/alerts`, `AlertService` (`internal/panel/orgs/alerts.go`). An org's admins and owners set **what** alerts and **who** hears about it (the org's settings, Alerts); each person sets **how** they're told (the bell, top right of every page, then its settings). Members don't see the org's alert settings.

- **The inbox (the bell).** Every alert sent to someone lands in their inbox, from every org they're in, newest first, kept 30 days: what happened, where (the server, its node, the org), and when, linking to the page it's about. A count of what's new since it was last opened sits on the bell (polled every 30 seconds); opening it marks them seen (`SeeNotifications`), and each can be dismissed, or all of them (`DismissNotifications`). Someone who leaves an org stops seeing its alerts.
- **People's own channels** (up to 10 each, `alert_channels.user_id`, `org_id` empty in the API): how a person is told besides the inbox. Everyone has an **email** channel to their account's address from the start (a trigger on `users`; it can be turned off or removed, and added back), and can add **Telegram** (Connect Telegram gives a link to a private chat with the bot, or a group), **ntfy** (phone push), **Discord**, **Slack**, or a **webhook**. Secrets are sealed to the person (`PANEL_DATA_KEY`, bound to their user ID), never shown again, and only they see or change their channels. One that fails for 3 days is turned off and they're emailed.
- **Team channels** (up to 20 an org) are places the whole team reads: **Discord** and **Slack** (incoming webhook URLs, only on their own hostnames), **Telegram** (a group with Raptor's bot: Connect Telegram makes a one-time link, valid 15 minutes, that adds it; `/start` with the link's token adds that chat as a channel), **ntfy** (a topic on ntfy.sh or another server, with an optional token), and **webhooks** (JSON, signed like Wings' were: `X-Raptor-Signature: sha256=<hex HMAC-SHA256 of "<timestamp>.<body>">` with `X-Raptor-Timestamp` and `X-Raptor-Event`). Email isn't a team channel: it goes to people, as they choose. Webhook URLs, the signing secret, ntfy topics, and tokens are sealed with `PANEL_DATA_KEY` (bound to the org) and never shown again; the app shows a hint. Every channel has a test button.
- **Rules** (up to 100) are a condition, where it applies (every node, one node, or one server), **who it tells**, and the team channels it also posts to (at least one of the two). People are picked by role (owners, admins, members) and by name; each is told in their inbox and on their own channels that are on. **Members** (not admins or owners) are only told about servers they have access to (a grant on that server, or for a node's alerts any server on it), so a rule can't tell them about what they can't see. Conditions: a server crashed, down after repeated crashes, install finished or failed, backup failed, restore finished or failed, over its disk limit; a node offline (after a grace, 5 minutes by default), its disk low, Wings updated or rolled back, signed actions and passkey changes (`security`); and thresholds: a server's CPU, memory, or disk above a percent, or the node's, for a while. Conditions that end (a node back online, disk space back, a threshold back under) say so too unless the rule says not to.
- **New orgs** start with rules telling their owners about crash loops, offline nodes, and low disk. (The migration to people, #291, turned each org's email channels into the people they reached, on the rules that used them, and gave everyone their own email channel.)
- **Where alerts come from.** Most are node events: the mirror hands each batch to `Ingest` in the transaction that applies it, which matches them against the node's org's rules and queues one delivery per channel, so an alert is queued exactly when its event is applied, once. Events more than 6 hours old when they arrive (a node cut off for a long time) don't alert. Thresholds are evaluated on the node, which has the metrics ([WINGS.md](WINGS.md#threshold-alerts)): the Panel sends each connected node its rules (`node.alerts`) when they change, it connects, or its Wings version changes, and they come back as `alert.fired` and `alert.resolved` events naming the rule. The Panel watches for nodes going offline itself: no live instance holding its connection, past the rule's grace counted both from when the node was last seen and from when this instance first noticed (so a Panel restart, which briefly leaves nodes held by nobody, isn't taken for nodes going offline).
- **Sending.** An alert is queued, in the same transaction, to the rule's team channels, into each of its people's inbox, and to each of their own channels that's on. One instance at a time sends (an advisory lock), every 3 seconds, a worker per channel. A send that fails is retried 3 times over about 12 minutes; a 4xx other than 429 (or a Telegram chat that's gone) isn't retried. At most 20 alerts a minute go to a channel; the rest are dropped and counted in the next one. A channel that fails every alert for 3 days is turned off and the org's owners are emailed; turning it back on clears that.
- **Never the Panel's own network:** channels connect only to public addresses, checked on every connection (DNS can change after a URL is saved), with no redirects followed and no proxy, so a webhook can't be pointed at the WireGuard link, Postgres, or a metadata address.
- **History:** every delivery, with its status and error, kept 30 days; the app shows the last 50. A delivery to someone's own channel is labelled with whose and which ("Alice: Email"), since admins can't see people's channels.

## Billing (Polar)

- **$12 / node / month.** First node free for the first month.
- Hosted add-ons (backup storage GB-months, relay GB later) are metered usage.
- **Every enrolled node is billed, regardless of status** (online, offline, or unreachable). A node stops being billed only when it's removed from the Panel.
- Implemented as a Polar **seat-based subscription** (`internal/panel/billing`, `BillingService`; #266), seats = the org's linked nodes. The org's ID is Polar's external customer ID, so the Panel never stores card or customer details.
  - **Starting:** an owner's "Start your free month" on Settings → Billing opens Polar's checkout (`StartCheckout`) for one seat per node (at least one), framed over the app (`components/billing/polar-embed.tsx`, the protocol of `@polar-sh/checkout` without the package) in the user's light or dark theme. Only messages from that frame and Polar's origin count; the frame stays hidden until Polar says it's loaded, and after 20 seconds without that the app sends the owner to Polar's own checkout page instead. Without a plan in force, no join tokens: Add Node sends owners to Billing first.
  - **Webhooks** (`POST /billing/polar/webhook` on the API, Standard Webhooks signatures, each delivery handled once) only say which subscription changed: the Panel fetches it from Polar, so deliveries out of order can't leave an old state. `billing_accounts` is the Panel's copy of what it last fetched.
  - **A sweep** every 5 minutes (one instance, by advisory lock; also right after a webhook or a node's removal, which waits for a running sweep instead of overlapping it, so an org's emails go once) fetches each subscription at least hourly, in case a webhook was lost, and keeps seats equal to nodes (`proration_behavior: prorate`, so a change shows up prorated on the next invoice). Removing the last node sets the plan to end with the period, and linking one again takes that back; an owner's own cancellation (here or in Polar's portal) is never undone.
  - **Owners** manage the rest on the same page, through Polar's customer portal API, called by the Panel with a customer session it makes for the org and keeps until 5 minutes before it runs out (an hour): saved cards (`GetBillingDetails`; `SetDefaultCard`, `RemoveCard`, which Polar refuses for the card a plan still needs), the last 24 charges with their invoices (`GetInvoice`: Polar makes an invoice the first time it's asked for, which takes seconds and can't be changed after), and cancelling with an optional reason, or taking it back (`CancelPlan`; not with no nodes, since it would renew for seats nothing uses). Adding a card is Polar's own form, framed in the page with a fresh customer session (`StartAddCard`); it becomes the default, and Polar charges a failed payment to it at once. Changing cards and cancelling need a recent re-authentication. The billing address, tax ID, and receipt email stay in Polar's portal (`OpenBillingPortal`), linked at the bottom. Members see the plan.
  - **One kind of change per update:** Polar's subscription update takes seats, cancelling, or the trial, never two at once, so the sweep sends seats first and the cancellation after.
- **Trial:** the first node is free for one month, **once per org**, and a **card is required at signup** (this cuts throwaway re-trial accounts). It's asked for at checkout only if the org hasn't had one and has at most one node. Linking a second node ends the free month (the sweep sets `trial_end` to now); Add Node says so first, and owners can end it early.
- **Raptor Backup Storage:** 10 GB included per paid node (pooled across the org; a trial node counts), then $0.012 per GB-month ($12/TB), metered daily from object storage usage. Backed by Backblaze B2 (about $6.95/TB): roughly 30% left after B2, payment fees, and API calls. During beta, a soft cap of 500 GB per org, raised on request.
- The Panel keeps its own **ledger** for reconciliation (`billing_ledger`): every subscription state it sees, orders paid and refunded with their amounts, the seat changes it made, and unpaid spells.
- Non-payment: 7-day grace → the Panel goes read-only for unpaid nodes → **servers keep running**. An org is unpaid when it has nodes and no plan that's trialing or active; a failed payment (`past_due`) counts from when it failed. Owners are emailed when it starts and when the nodes go read-only, and every page of the org says when. Read-only means `CommandService` refuses everything but reads (and ending support access), and no join tokens; the org can still remove nodes and pay. Hosted backup data is kept 30 days, then deleted after email reminders.
- **Not yet:** charging for Raptor Backup Storage over the included 10 GB (it's measured, and shown as an estimate).

## Staff

Raptor's own admin panel, at `/admin` in the app (`internal/panel/staff`, `StaffService`), for staff only. Staff switch to it like to an org: **Raptor admin** is the last entry in the org switcher. On admin pages the switcher shows "Raptor admin" with the staff role, and picking an org from it goes back. The sidebar then has the admin sections instead of the org's: Overview, Users, Orgs, Support, Eggs, Announcements, and Settings (the staff and the staff log). Org pages never show the admin sections.

**Who.** Staff are **admins** (look and act) or **support** (look). They're made only on the Panel's server, with `panel staff add <email> admin|support` (and `remove`, `list`), never from the web, so a staff account that's taken over can't make more. The account must already exist and needs a passkey.

**Getting in.** Every call needs a passkey confirmed by the session in the last **12 hours** (`sessions.passkey_at`, set by signing in or confirming with one). Every change needs one in the last **5 minutes**, through the usual "confirm it's you" dialog. An emailed code or an authenticator app isn't enough. Non-staff get nothing, and `GetStaffMe` just says they aren't staff.

**What it shows.** The Overview has tabs: **Summary** (everything at a glance, below), and **Users**, **Nodes**, **Billing**, and **Infrastructure** in depth:
- **Users:**
  - accounts, and how many are active today, this week, and this month;
  - passkey and authenticator app adoption;
  - suspended accounts, accounts in no org, open invitations;
  - accounts, sign-ups, and accounts signing in per day;
  - sign-in methods over 30 days, linked accounts, and the largest orgs.
- **Nodes:**
  - connected of linked;
  - total cores and memory, and servers per node;
  - nodes and servers over time;
  - operating systems, Wings versions, architectures, server states, and the most deployed games;
  - offline nodes and failing servers.
- **Billing:** recurring revenue (seats on active and past-due plans at the node price), paying orgs, orgs on the free month, unpaid and read-only orgs, orgs by plan status, and orders paid less refunds by month from `billing_ledger`; then Raptor Backup Storage, which isn't charged yet: what it would bill a month, storage over time, and each org's storage. With billing off, it says so.
- **Infrastructure:**
  - the Panel's instances and the node connections each holds;
  - the database's size, connections, and largest tables;
  - live sessions, this Panel's version, and the latest Wings rollout.

  Grafana has the rest (latency, errors, the host).

The Summary:
- **Overview:** nodes connected, servers running, users (new and active in 30 days), orgs, and Raptor Backup Storage, with their history (counted every hour into `platform_stats` by one Panel instance) over 7 days to a year; sign-ups per day; Wings versions, architectures, and the most deployed games; offline nodes (gone over an hour) and crashed or failed servers.
- **Revenue:** recurring revenue from paid plans, paying orgs and free months, and what was paid in the last 30 days; and what Raptor Backup Storage would bill a month, said as not charged yet.
- **Users and orgs:** search, and each one's details: orgs, members, nodes, recent sign-ins, Backup Storage.

**What it can do** (admins):
- **Suspend an account** (with a reason): it's signed out everywhere and can't sign in. Its orgs and servers keep running.
- **Sign an account out everywhere.**
- **Suspend an org** (with a reason): the Panel refuses to manage it (org pages, commands, the console, and SFTP logins), and its members see why. Its nodes and game servers keep running, so players aren't punished for the owner's account.

Staff can't sign in as a user, and can't read or act on servers. Reaching a node needs support access, which its owner signs (below).

**The staff log** (`staff_audit`) records every change and every account or org opened (once per 10 minutes per staff member), with who and from where. A trigger refuses updates, deletes, and truncation, so staff can't edit it. Suspensions also go in the affected account's or org's own log, as done by staff.

## Announcements

Staff publish banners everyone signed in sees, under the app's header (admin panel → Announcements; `SaveAnnouncement`, `EndAnnouncement`, admins, logged).
- Each is news, a warning (an incident), or maintenance, with a title, up to 1,000 characters of **plain text** (rendered as text, never HTML or Markdown), and an optional `https://` link.
- It shows from its start (now by default) to its end (until ended by default). Maintenance comes first, then warnings.
- People can dismiss one; that's remembered in their browser.
- `ListActiveAnnouncements` is open to anyone signed in. The app asks every 5 minutes.

## Support access

Raptor's staff can work on a node only with its owner's consent, signed with the owner's passkey and enforced by Wings.

1. **Staff ask** from the admin panel (an org's page → a node → "Ask for access"; `RequestSupportAccess`, staff with a passkey confirmed in the last 5 minutes). A request carries a level, a duration (15 minutes to 7 days), a reason, and a **ticket reference**. The org's owners and admins are emailed, and the node's pages show it to them. It goes in the org's log and the staff log.
2. **The owner decides.** Denying (`DenySupportRequest`) needs no passkey. Approving signs `support.grant` with an owner's passkey (never a delegation): the request's ID, the staff member, the level, and the end time. The owner can approve less than was asked, but not more and not for someone else; the Panel checks that against the request before sending. Wings keeps the grant (`support_grants`), and the Panel records the request as approved once the node accepts it.
3. **Staff act under it** through `CommandService.Execute` with `support` set to the request. The Panel checks that the caller is staff (passkey within 12 hours), and that the request is theirs, for this node, approved, unexpired, and its level covers the action. It puts the request's ID in the grant it signs (`Grant.Support`). Wings checks every such command again against the grant the owner signed, and refuses anything the grant doesn't cover, anything over or revoked, and anything passkey-signed. The levels, shared by both (`nodecmd.SupportLevel`):
   - **1, diagnostics:** health, doctor's checks, metrics, disks, system settings, Wings' own log (`node.logs`), servers' metrics and ports, backup destinations and activity, trusted keys.
   - **2, operate:** level 1, plus starting, stopping, restarting, and killing servers, and their consoles.
   - **3, manage:** level 2, plus servers' files (not permanent deletes), schedules, and backing up.

   The support session page (`/admin/support/<request>`) has what the level allows: health and checks, Wings' log, and servers' power, plus a server's live console at level 2 and its file manager at level 3. The console stream takes the request too (the live socket's `console` op with `support`), checked like a command (`server.console` is level 2), and each opening is logged like a staff command. Files have no uploads or downloads, which go through file transfers that support access doesn't cover.
4. **While it lasts,** the node's pages show who has what until when, with "End now". Every staff command, even a look, goes in the org's log as done by staff, in the staff log, and in Wings' own log.
5. **Ending it:** "End now" (`support.revoke`, not signed: it only takes access away), `sudo raptor support revoke <id>|all` on the box, or turning support access off (node Settings, or `sudo raptor support off`), which ends every grant and refuses new ones until it's turned on again.

**There is no shell access for staff,** and no action that needs the owner's passkey. Support uses typed commands only.

The limit, said plainly: this stops a staff account (or one taken over) from reaching a node without the owner, and makes everything staff do provable on the node. A fully compromised Panel could still send the *unsigned* commands any owner could, as the owner, which is no different from before support access existed. Signed actions stay out of reach.

## Beginner-focused features

- **Connection test:** on demand, from the server's Network tab. The Panel (`OrgService.TestConnection`, `internal/panel/orgs/connection.go`) opens a TCP connection to each of the server's ports (at most 16, in parallel, 5 seconds each) on the node's public IPv4, or IPv6 without one. A connection that's accepted and then closed within 750 ms is "closed", not reachable: Docker's port forwarder accepts even with nothing behind it, and a game waits for the player to speak. Allocations on 127.0.0.1 aren't probed ("local_only"). At the same time the browser asks the node what the game listens on (`server.ports`, a read with `View`), and `web/src/lib/connection-test.ts` puts both into one cause per port: reachable, UDP only (a TCP probe can't tell), not running, nothing listening yet, listening on other ports (named), listening on 127.0.0.1 only, or listening but not reachable: a firewall. A firewall gets a step-by-step guide for the provider, guessed from the node's reverse DNS (Hetzner, OVH, AWS, Google Cloud, Azure, Oracle, DigitalOcean, Linode, Vultr, a home connection) and changeable, with the port filled in. Ten tests a minute per user. Not yet: running it after a server's first start.
- **Node DNS:** the Panel creates `<short-id>.raptornodes.net` (the short ID is the node ID's last part) at enrollment and updates its A/AAAA records whenever Wings reports a new public IP. Deleted nodes' names are never reused. The zone is only for nodes: player subdomains were removed (#279), and their records deleted.
- **Safe defaults:** backups on, crash restart on.
- **Node health page:** security and configuration warnings reported by `doctor`.

## AGPL obligations

The Panel links to the **exact source commit** that is deployed: the sidebar's footer and the sign-in page show "Source code", pointing at the commit the API reports (`MetaService.GetVersion`, set by the image build's `COMMIT`), or the repository for development builds. All dependencies must be AGPL-compatible (CI checks licenses).

## Hosting

- A primary server, deployed with Docker Compose: Caddy and two Panel instances, so deploys replace one at a time (#200). The hosting provider is an operational choice and is intentionally not fixed in these docs. The files are in `deploy/`, and [DEPLOY.md](DEPLOY.md) is the step-by-step guide.
- Postgres with WAL archiving via pgBackRest to **object storage in a different location**, plus a **streaming replica on a second server** at launch (see [RELIABILITY.md](RELIABILITY.md)).
- Hostnames are listed in [ARCHITECTURE.md](ARCHITECTURE.md#hostnames). `api.raptorpanel.net` is behind the **Cloudflare proxy** (browsers and node connections alike); the origin only accepts traffic from Cloudflare (per-hostname Authenticated Origin Pulls with our own certificate, and a firewall admitting Cloudflare's addresses; #201). Caddy terminates TLS at the origin.
- The web app, landing page, and docs are static sites on separate hosting from the Panel servers (Cloudflare Workers static assets; #203, #211); the web app's deploy credentials are separate from the Panel servers' (the landing page shares them while it deploys from the same release; #261). The app and the Turnstile page send strict Content-Security-Policy headers (#206).
- Secrets are in 1Password only, written to memory at boot by a read-only service account (#202).
- `raptornodes.net` is **DNS-only**, on a plan sized for the record count, and goes on the Public Suffix List before launch (see [ARCHITECTURE.md](ARCHITECTURE.md#node-dns)). Nothing of ours is hosted on it.
- The status page (`status.raptorpanel.net`) is hosted with a different provider than the Panel, and its DNS doesn't depend on Cloudflare.
- **Account security:** the registrar and Cloudflare accounts use hardware-key 2FA with no SMS recovery, the domains have registrar lock, and API tokens are scoped (the web app's deploy token can only deploy Workers; the Panel servers hold one Cloudflare token, which can only edit `raptornodes.net`'s DNS records, from the servers' addresses). Whoever controls DNS or the static host controls the code users run.
