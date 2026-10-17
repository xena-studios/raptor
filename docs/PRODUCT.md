# Raptor: Product Outline

## What it is

**Raptor is a game server management platform.** You bring your own Linux server (a dedicated box, a VPS, a home server), run one command, and it becomes a reliable game server host that you manage from the web panel at **raptorpanel.net**.

It does the same job as Pterodactyl and Pelican, but as a **hosted service** instead of software you install and maintain. There's no panel to set up, no web server, database, or TLS certificates to configure, and nothing to keep updated. You add machines and run servers.

Raptor is **fully open source (AGPL-3.0)**.

## Who it's for

People who **own or rent a Linux box but don't have a reliable, easy way to host things on it**:

- Communities and friend groups running a Minecraft, Hytale, or Rust server
- Server networks running many game servers across a few machines
- Anyone with a dedicated server or VPS who doesn't want to become a Linux admin to use it

**Not for:** hosting companies reselling servers. Raptor has no white-labeling, reseller features, or billing-system integrations (WHMCS, Paymenter, Blesta), and none are planned.

## What you can host

- **Game servers (primary focus):** the Minecraft family (Paper, Purpur, Fabric, Forge, NeoForge, Vanilla, Velocity, BungeeCord), Rust, popular Steam games, and Hytale (community egg; see [EGGS.md](EGGS.md#certified-at-launch))
- **Also officially supported:** Discord bots (Node.js/Python), voice servers (TeamSpeak, Mumble), generic Node/Python apps

Anything with a Pterodactyl or Pelican egg can run. The list above is what we **certify** (test in CI and document). Raptor is not a general app platform.

## How it works

```
   You                          raptorpanel.net                 Your Linux box
    │                                 │                               │
    │ 1. Sign up, click "Add Node"    │                               │
    │────────────────────────────────►│                               │
    │ 2. Get a one-line command       │                               │
    │◄────────────────────────────────│                               │
    │ 3. Paste it on your box ───────────────────────────────────────►│
    │                                 │   4. Wings installs itself,   │
    │                                 │◄════ connects securely ═══════│
    │ 5. "Node connected ✓"           │                               │
    │◄────────────────────────────────│                               │
    │ 6. Create a Minecraft server ──►│──── Wings sets it up ────────►│
    │                                 │                               │
                        Players connect directly to your box ◄────────┘
```

**Panel** (the website, hosted by Raptor)
The dashboard for everything: servers, console, files, backups, schedules, team members, billing.

**Wings** (the daemon, runs on your box)
Does the actual work: runs servers in Docker containers, runs schedules and backups, restarts crashed servers, enforces disk limits, serves SFTP. **Wings is self-sufficient once set up.** If the Panel goes offline, your servers keep running, schedules keep firing, and backups keep happening.

## Features

**Setup**
- One-command install that connects the box to the Panel automatically
- Wings connects out to Raptor, so there's no management port to open
- Automatic updates, with the option to pin a version
- Runs alongside an existing Pterodactyl install without interfering with it

**Servers**
- Native Pterodactyl and Pelican egg support
- Live console, resource graphs, player counts
- Crash detection and automatic restart, on by default
- Real disk limits enforced by the kernel, with instant usage reporting
- **Connection test:** the Panel checks from outside whether players can reach your server, and tells you how to fix your provider's firewall if they can't

**Files**
- Web file manager with a code editor, uploads and downloads up to 1 GB per file
- SFTP directly to your box at full speed for anything bigger (turn it on per node)
- Every node gets its own hostname (`1cdd002cd9a3.raptornodes.net`) that follows your IP, even on a home connection

**Automation**
- Multi-step schedules (warn players → wait → back up → restart) in your timezone
- Backups on by default: incremental, deduplicated, encrypted. Stored on the box, in your own S3/B2, or in Raptor's hosted storage.
- Alerts to people and team channels: a server down or crashing, an install finished, a node offline, disk low, CPU or memory above a threshold, and every signed dangerous action. Admins pick who hears about what; each person picks how (an inbox under the bell, plus their email, Telegram, ntfy, Discord, Slack, or a webhook). New orgs start with their owners told about the serious ones.

**Teams**
- Organizations with members and roles
- Per-server sub-users with fine-grained permissions
- Full audit log

**Command line**
- The `raptor` CLI and a terminal UI on the box for **keeping things running**: status, start/stop/restart, live console, logs, backup restore
- `raptor doctor` diagnoses problems and explains how to fix them
- All setup and configuration happens in the web panel

**Support**
- Support can request temporary, scoped access to your node. You approve it, see everything they do, and can revoke it at any time.
- If your node can't connect, `raptor doctor --bundle --upload` sends a redacted diagnostics bundle.

**Migration**
- `raptor import pterodactyl` moves existing Pterodactyl servers in place, with the same files and eggs

**Node health**
- Warnings (never automatic changes) about risky box setup: password root login, disabled security updates, low disk, clock drift

## Pricing

| | |
|---|---|
| **Per node** | **$12 / month** for every node linked to your account, online or not |
| **First node** | Free for the first month (card required at signup) |
| **Raptor Backup Storage** | **10 GB included with every paid node** (pooled across the org), then $12 per TB-month, billed by the GB ($0.012/GB) |
| **Connection relay** | Later, usage-based |

Every node includes unlimited servers, users, schedules, and all features. There are no limits on RAM, CPU, or players, because it's your hardware.

**If you stop paying**, the web panel goes read-only for unpaid nodes. **Raptor never shuts down your servers over billing.** Wings and the local CLI keep working.

Billing runs through Polar (merchant of record), which handles VAT and sales tax.

## Why Raptor instead of Pterodactyl or Pelican

| | Pterodactyl / Pelican | Raptor |
|---|---|---|
| Panel | You install and maintain it | Hosted, nothing to maintain |
| Node setup | Configure wings, TLS, ports, DNS | One command, no ports to open for management, a hostname included |
| Panel goes down | Nodes can't be managed | Servers, schedules, and backups keep running |
| Local control | None | CLI + TUI on the box |
| Disk limits | Soft, scan-based | Kernel-enforced, instant |
| Backups | Full copies | Incremental, deduplicated, encrypted |
| "Players can't connect" | You figure it out | Built-in connection test with provider guides |
| Eggs | ✓ | ✓ fully compatible |
| License | MIT / AGPL | AGPL |

## Security principles

- Wings only accepts specific, typed commands. It never runs arbitrary shell commands sent to it.
- No passwords: sign in with a passkey, Google, Discord, GitHub, or a code sent to your email, with authenticator-app 2FA.
- **Your box only obeys your passkey for dangerous actions.** Deleting servers, changing what code runs, or giving anyone access must be signed by your own passkey, and your box checks that signature itself. Even if Raptor's servers were hacked, nobody could wipe or backdoor your servers without your fingerprint.
- Your box connects out to Raptor; it opens no management ports. Both sides prove their identity with signed keys, and your box's key is generated on it and never leaves it.
- Raptor's site runs behind Cloudflare, which can see traffic passing through the web panel. For private file transfers, use SFTP, which goes straight to your box.
- Wings releases are cryptographically signed.
- Backups are encrypted on your box before upload.
- Servers run in hardened, isolated containers.
- Support access requires your approval (signed with your passkey), is time-limited, and is fully audited.
- All the code is public and auditable.

## Domains

| Domain | Use |
|---|---|
| `raptorpanel.net` | Landing page; web app (`app.`), API and node connections (`api.`), docs (`docs.`), installer (`get.`), status page (`status.`) |
| `raptornodes.net` | Node hostnames (`1cdd002cd9a3.raptornodes.net`), nothing else |

## Register

product

## Design brief

The web app is an operational tool, so design serves the task. [DESIGN.md](DESIGN.md) is the visual system; this is the why behind it.

**The moment it's built for:** a community owner glancing at whether their server is up, on a laptop or a phone, wanting confidence in a beat. They're capable but short on time and don't speak Docker or Linux, and they shouldn't have to.

**Personality:** calm, precise, trustworthy. An instrument panel you'd leave open all day: a steady console that tells you the truth at a glance.

**Voice:** plain and direct. Name things the way owners think (server, node, egg, port), never the way Docker does (container, image, bind mount). Errors say what happened and what to do, never blame, and never show developer text. Destructive actions say what's lost and ask once. No em dashes and no marketing adjectives in the app.

**Anti-references:**
- Developer-terminal cosplay: neon, glow, Matrix green. The mono type and dark ink carry meaning (state, identity, data), not "hacker" decoration.
- Toy dashboards: big hero numbers, confetti, rounded pills everywhere.
- Generic SaaS: soft shadows, lavender gradients, friendly blobs.
- Over-confirmation: a dialog for every action. Inline first; confirm only what's destructive or irreversible.

**Principles:**
1. Colour carries state, never decoration. One red is the only thing that lights up; green, amber, and red appear only to say how something is.
2. Hairlines over shadows. Grouping comes from rules and tint.
3. Mono is structure. Data, labels, and states read as console readouts.
4. Secure by default is visible. Guard rails (protected ports, passkey signatures, install-script reviews) are shown, not hidden.
5. Eggs over images. People pick games; raw image names never surface.

**Accessibility:** WCAG 2.2 AA. Text meets 4.5:1 (3:1 for large text) in both themes; state is always spelled out, never colour alone; everything works from the keyboard with a visible focus ring; motion respects `prefers-reduced-motion`.
