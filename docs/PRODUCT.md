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

- **Game servers (primary focus):** the Minecraft family (Paper, Purpur, Fabric, Forge, NeoForge, Vanilla, Velocity, BungeeCord), Hytale, Rust, and popular Steam games
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
- **Free subdomains** with SRV records, so players type `smp.raptornodes.net` instead of an IP and port

**Files**
- Web file manager with a code editor, uploads and downloads up to 1 GB per file
- SFTP directly to your box at full speed for anything bigger (turn it on per node)
- Every node gets its own hostname (`n-k7m2qx9d.raptornodes.net`) that follows your IP, even on a home connection

**Automation**
- Multi-step schedules (warn players → wait → back up → restart) in your timezone
- Backups on by default: incremental, deduplicated, encrypted. Stored on the box, in your own S3/B2, or in Raptor's hosted storage.
- Discord and webhook alerts sent directly from your box

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
| **Hosted backup storage** | $0.02 per GB-month |
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
| `raptornodes.net` | Node hostnames (`n-k7m2qx9d.raptornodes.net`) and player-facing server subdomains (`smp.raptornodes.net`) |
