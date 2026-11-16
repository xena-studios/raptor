---
title: Game panels compared
description: Raptor beside Calagopus, FeatherPanel, Hydrodactyl, Pelican, Pterodactyl, and PufferPanel, row by row, with a source for every cell and where each one does better than Raptor.
sidebar:
  order: 5
---

There are more game server panels than ever. This page puts Raptor beside the six people ask us about most, row by row, so you can pick the right one, even if that isn't Raptor. The short version is a table at [raptorpanel.net/compare](https://raptorpanel.net/compare/); this is the long one.

## How we checked

Everything here comes from each project's own website, docs, source code, and GitHub releases, as of **November 16, 2026**. Every cell links to where we found it. Comparison tables a project publishes about its rivals weren't used as evidence about the rivals.

- **None found** means we searched the project's docs and code and found nothing. That isn't proof it's missing; tell us if we missed it.
- **Latest release** and **GitHub stars** change weekly; they're a snapshot.
- Paid add-ons and third-party extensions are noted as such: they're real, but they aren't part of the panel.

Spotted a mistake? Tell us on [Discord](https://discord.gg/HyUa3sRpeN) and we'll fix it.

## At a glance

| | Raptor | Calagopus | FeatherPanel | Hydrodactyl | Pelican | Pterodactyl | PufferPanel |
|---|---|---|---|---|---|---|---|
| Who runs the panel | We do | You do | You do, or their Cloud | You do | You do | You do | You do |
| Licence | AGPL-3.0 | MIT | AGPL-3.0 | Apache-2.0 | AGPL-3.0 | MIT | Apache-2.0 |
| Price | $12 a machine a month | Free | Free; paid Premium | Free | Free | Free; add-ons paid | Free |
| Ports to open on a node | None | 8080, 2022 | 8080, 2022 | 8080, 2022 | 8080, 2022 | 8080, 2022 | 8080, 5657 |
| Pterodactyl eggs | Run as they are | Run as they are | Imported | Run as they are | Imported | Its own | No |
| Plugin and mod installer | Built in | Third-party extension | None found | Built in, Minecraft | Free official plugin | Paid add-ons | No |
| Steam Workshop | Built in | None found | None found | No | No | Paid add-ons | No |
| Backups | Incremental, encrypted | Many drivers, dedup | Full archives | Full archives | Full archives | Full archives | Full archive |
| Database browser | Every engine | Built in | phpMyAdmin | No | No | No | No |
| Disk limits | Kernel quotas | Quotas if set up | Scans; quotas optional | Scans | Scans; quotas in progress | Scans | None found |
| Passkeys | Yes | Yes | Yes | No | Yes | No | No |
| Alerts | Six channels | None found | Email, webhooks | Discord, Slack, Mattermost | Webhooks | None found | None found |
| Move from Pterodactyl | Yes | Yes | Yes | Yes | Yes | — | No |

## Raptor

A hosted panel: we run it at app.raptorpanel.net, and your machines run the games. All of it is open source, the panel and what runs on your machine.

| | Raptor |
|---|---|
| What it is | A hosted panel and its own node software, written in Go, with a React web app |
| Who runs the panel | We do |
| Licence | AGPL-3.0, [the source](https://github.com/xena-studios/raptor) |
| Price | $12 a month per machine, every server included; the first month is free. See [Pricing](/start/pricing/) |
| To install | Nothing for the panel. One command per node, which installs Docker and Raptor. See [Connect your first node](/install/first-node/) |
| Ports to open on a node | None for Raptor: the node connects out. SFTP's port opens only while someone uses it |
| Pterodactyl eggs | Run as they are, beside Raptor's own library of 350+. See [The egg library](/eggs/library/) |
| Plugins and mods | Built in: Modrinth, Hangar, and CurseForge for Minecraft, uMod for Rust, the Factorio mod portal; dependencies and Update all. See [Plugins, mods, and the Workshop](/servers/addons/) |
| Modpacks | Modrinth, CurseForge, Feed The Beast, and Technic |
| Steam Workshop | Built in, for Garry's Mod, Counter-Strike 2, ARK, Conan Exiles, and Squad |
| Extensions | None: what others add with plugins is built in. You can't add your own code to the panel |
| Backups | Incremental, deduplicated, and encrypted on the machine; to the machine, S3, B2, SFTP, or Raptor Backup Storage. See [Backups](/servers/backups/) |
| Databases | MariaDB, MySQL, PostgreSQL, Redis, Valkey, and MongoDB, each with a browser. See [Database instances](/nodes/databases/) |
| Disk limits | Enforced by the kernel (XFS quotas) as files are written. See [Disks](/nodes/disks/) |
| Sign-in | Passkeys, authenticator apps, Google, GitHub, Discord; dangerous actions are signed with your passkey and checked by your machine. See [How Raptor protects you](/security/how-raptor-protects-you/) |
| Alerts | Email, Discord, Slack, Telegram, ntfy, and webhooks. See [Alerts](/team/alerts/) |
| Isolation | Docker; Linux nodes only (Debian 12 or 13, Ubuntu 24.04) |
| From Pterodactyl | `raptor import pterodactyl`, on the same machine, beside it. See [Moving from Pterodactyl](/install/from-pterodactyl/) |
| Latest release | 0.17.0, November 15, 2026: early access |

**Where Raptor falls short:** you can't host the panel yourself, so you depend on us for management (your servers, schedules, and backups keep running without us). It costs money where the others are free. There's no extension system, so you can't add your own panel features. It's young: early access, at version 0.17.

## Calagopus

A rewrite of Pterodactyl from scratch in Rust, using the same eggs. It's free, MIT-licensed, very active, and the closest of these to Raptor in features.

| | Calagopus | Source |
|---|---|---|
| What it is | A Rust rewrite of Pterodactyl, with the same egg format | [README](https://github.com/calagopus/panel#readme) |
| Who runs the panel | You do | [Installation](https://calagopus.com/docs/panel/installation) |
| Licence | MIT, panel and Wings | [LICENSE](https://github.com/calagopus/panel/blob/main/LICENSE) |
| Price | Free, including commercial use | [What is Calagopus](https://calagopus.com/docs/about/what-is-calagopus) |
| Stack | Rust (Axum) and React; node software is Calagopus Wings, in Rust | [Wings](https://github.com/calagopus/wings) |
| To install | PostgreSQL and Valkey or Redis, no PHP; Docker (needed for extensions), packages, or a single binary | [Binary install](https://calagopus.com/docs/panel/installation/binary) |
| Ports to open on a node | 8080 for the API, 2022 for SFTP | [Wings configuration](https://calagopus.com/docs/wings/configuration) |
| Pterodactyl eggs | Run as they are | [README](https://github.com/calagopus/panel#readme) |
| Plugins and mods | Not built in; a third-party extension adds Modrinth. No Steam Workshop found | [content-installer](https://github.com/Regrave/content-installer) |
| Extensions | Its own, for the backend (Rust) and the web app | [Extensions](https://calagopus.com/docs/panel/extensions) |
| Backups | Eight drivers: a local archive, deduplicated ddup-bak, Btrfs and ZFS snapshots, S3, Restic, Kopia, and Proxmox Backup Server. Encryption through Restic, Kopia, or PBS | [Features](https://github.com/calagopus/website/blob/main/.vitepress/data/features.ts) |
| Databases | MySQL, MariaDB, PostgreSQL, MongoDB, and Redis, with a database explorer and SQL console | [Features](https://calagopus.com/docs/about/features) |
| Disk limits | Filesystem quotas (Btrfs, ZFS, XFS) when set up; by default, none, and usage is scanned | [Wings configuration](https://calagopus.com/docs/wings/configuration) |
| Sign-in | Authenticator apps, passkeys, OAuth | [Features](https://calagopus.com/docs/about/features) |
| Alerts | None built in found; schedules can make HTTP requests | [Schedules](https://calagopus.com/docs/panel/features/server/schedules) |
| Isolation | Docker; the panel runs on Windows, nodes need Linux | [What is Calagopus](https://calagopus.com/docs/about/what-is-calagopus) |
| From Pterodactyl | A built-in importer, from Pelican too | [Migrating](https://calagopus.com/docs/additional/migrations/pterodactyl) |
| Latest release | 1.2.10, November 15, 2026, stable | [Releases](https://github.com/calagopus/panel/releases) |
| GitHub stars | About 910 | [calagopus/panel](https://github.com/calagopus/panel) |

**Where it beats Raptor:** it's free and MIT, you host it yourself, it has an extension system, and its backups support more destinations (snapshots, Restic, Kopia, PBS).
**Where Raptor is ahead:** no panel to run and no ports to open, add-ons and the Workshop built in, more database engines, encrypted backups by default, kernel disk limits by default, alerts, and passkey-signed actions.

## FeatherPanel

A PHP panel from MythicalSystems for game servers, Proxmox VMs, and web hosting, with its own node software, FeatherWings, a fork of Pterodactyl's Wings.

| | FeatherPanel | Source |
|---|---|---|
| What it is | Its own PHP panel; FeatherWings forks Pterodactyl's Wings | [FeatherWings licence](https://github.com/MythicalLTD/FeatherWings/blob/main/LICENSE) |
| Who runs the panel | You do; an optional managed Cloud attaches your own nodes | [featherpanel.com](https://featherpanel.com) |
| Licence | AGPL-3.0 (the panel's LICENSE file; one package file says MIT), FeatherWings MIT | [LICENSE](https://github.com/MythicalLTD/FeatherPanel/blob/develop/LICENSE) |
| Price | Free to host; a paid Premium removes branding and raises limits. We couldn't confirm its price | [Premium](https://github.com/MythicalLTD/FeatherPanel/blob/develop/backend/app/Services/FeatherCloud/FeatherPanelPremium.php) |
| Stack | PHP 8.5 (Symfony components) and Next.js, a Rust job runner; FeatherWings in Go | [README](https://github.com/MythicalLTD/FeatherPanel#readme) |
| To install | Docker Compose with MariaDB, Redis, and its services; a script sets up NGINX, Apache, or a Cloudflare Tunnel | [docker-compose.yml](https://github.com/MythicalLTD/FeatherPanel/blob/develop/docker-compose.yml) |
| Ports to open on a node | 8080 for the API, 2022 for SFTP | [Config](https://github.com/MythicalLTD/FeatherWings/blob/main/config/config.go) |
| Pterodactyl eggs | Imported, as "spells" | [Spells](https://github.com/MythicalLTD/FeatherPanel/blob/develop/backend/app/Controllers/Admin/SpellsController.php) |
| Plugins and mods | None found in the panel; no Steam Workshop | [Repository](https://github.com/MythicalLTD/FeatherPanel) |
| Extensions | Its own plugins, with a marketplace | [Plugins](https://mythicalltd.github.io/FeatherPanel/icanhasfeatherpanel/plugins/) |
| Backups | Full archives to the machine or S3, and Proxmox Backup Server (with its encryption) | [Backups](https://github.com/MythicalLTD/FeatherWings/tree/main/server/backup) |
| Databases | MySQL, MariaDB, PostgreSQL, MongoDB, and Redis hosts; phpMyAdmin with single sign-on | [Changelog](https://github.com/MythicalLTD/FeatherPanel/blob/develop/CHANGELOG.md) |
| Disk limits | Scanned by default; XFS or ext4 quotas optional | [Quotas](https://github.com/MythicalLTD/FeatherWings/tree/main/server/filesystem/quotas) |
| Sign-in | Authenticator apps, passkeys, OIDC, LDAP, Discord | [Auth](https://github.com/MythicalLTD/FeatherPanel/tree/develop/backend/app/Controllers/User/Auth) |
| Alerts | Node alerts to an inbox, email, and webhooks | [Node alerts](https://github.com/MythicalLTD/FeatherPanel/blob/develop/backend/app/Services/NodeAlerts/NodeAlertPolicy.php) |
| Isolation | Docker; Linux nodes | [README](https://github.com/MythicalLTD/FeatherWings#readme) |
| From Pterodactyl | An importer (into a fresh panel) and a CLI | [FeatherPanel-CLI](https://github.com/MythicalLTD/FeatherPanel-CLI) |
| Latest release | 1.5.4, November 12, 2026, stable | [Releases](https://github.com/MythicalLTD/FeatherPanel/releases) |
| GitHub stars | About 250 | [MythicalLTD/FeatherPanel](https://github.com/MythicalLTD/FeatherPanel) |

**Where it beats Raptor:** it does more than game servers (VMs, web hosting, tickets), it's free to host, it has plugins, and it signs in with LDAP and OIDC.
**Where Raptor is ahead:** no panel to run or ports to open, add-ons built in, incremental encrypted backups, a browser for every engine, kernel disk limits by default, and more alert channels.

## Hydrodactyl

A fork of Pterodactyl, by way of Pyrodactyl, funded by Blueprint. It can use Pterodactyl's Wings, Elytra, or Calagopus's Wings on its nodes.

| | Hydrodactyl | Source |
|---|---|---|
| What it is | A fork of Pterodactyl, via Pyrodactyl | [README](https://github.com/BlueprintFramework/hydrodactyl#readme) |
| Who runs the panel | You do | [Installation](https://hydrodactyl.dev/docs/hydrodactyl/installation) |
| Licence | Apache-2.0 | [LICENSE](https://github.com/BlueprintFramework/hydrodactyl/blob/main/LICENSE.md) |
| Price | Free, with donations | [README](https://github.com/BlueprintFramework/hydrodactyl#readme) |
| Stack | Laravel (PHP 8.4) and React 19; nodes run Pterodactyl's Wings by default, or Elytra, or Calagopus Wings | [Daemon types](https://github.com/BlueprintFramework/hydrodactyl/blob/main/app/Enums/Daemon/DaemonType.php) |
| To install | PHP 8.4, Composer, pnpm, MariaDB, MySQL, or PostgreSQL, Redis, and a web server; or Docker | [Native install](https://hydrodactyl.dev/docs/hydrodactyl/installation/native-install/native) |
| Ports to open on a node | 8080 and 2022, as Wings | [Docs](https://hydrodactyl.dev/docs/hydrodactyl) |
| Pterodactyl eggs | Run as they are | [Repository](https://github.com/BlueprintFramework/hydrodactyl) |
| Plugins and mods | A built-in Marketplace for Minecraft: Modrinth, Hangar, and Spiget. No Steam Workshop | [Marketplace sources](https://github.com/BlueprintFramework/hydrodactyl/tree/main/app/Services/Marketplace/Sources) |
| Extensions | None; Blueprint extensions don't work on it | [README](https://github.com/BlueprintFramework/hydrodactyl#readme) |
| Backups | Full archives to the machine or S3; with Elytra, rustic | [Backups config](https://github.com/BlueprintFramework/hydrodactyl/blob/main/config/backups.php) |
| Databases | MySQL and MariaDB hosts; no browser | [Repository](https://github.com/BlueprintFramework/hydrodactyl) |
| Disk limits | Scanned, with Pterodactyl's Wings; depends on the node software | [Wings](https://github.com/pterodactyl/wings) |
| Sign-in | Authenticator apps; no passkeys found | [Client API](https://github.com/BlueprintFramework/hydrodactyl/tree/main/app/Http/Controllers/Api/Client) |
| Alerts | Per-server activity webhooks to Discord, Slack, and Mattermost | [Webhook config](https://github.com/BlueprintFramework/hydrodactyl/blob/main/config/webhook.php) |
| Isolation | Docker; Linux nodes | [README](https://github.com/BlueprintFramework/hydrodactyl#readme) |
| From Pterodactyl | Migrates in place, with guides for Docker and native installs | [Migrations](https://hydrodactyl.dev/docs/hydrodactyl/migrations) |
| Latest release | 6.4.2, October 21, 2026; its README calls it pre-release software | [Releases](https://github.com/BlueprintFramework/hydrodactyl/releases) |
| GitHub stars | About 270 | [BlueprintFramework/hydrodactyl](https://github.com/BlueprintFramework/hydrodactyl) |

**Where it beats Raptor:** it's free, you host it, it reads Spiget for plugins, and it's a modern take on Pterodactyl you can switch to in place.
**Where Raptor is ahead:** no panel to run or ports to open, mods beyond Minecraft and the Workshop, incremental encrypted backups, more databases with a browser, kernel disk limits, passkeys, and more alert channels.

## Pelican

A fork of Pterodactyl, rebuilt on Laravel and Filament, with first-party plugins. It's at its 1.0 release candidate.

| | Pelican | Source |
|---|---|---|
| What it is | A fork of Pterodactyl on Laravel and Filament | [Comparison](https://pelican.dev/docs/comparison) |
| Who runs the panel | You do | [Getting started](https://pelican.dev/docs/panel/getting-started) |
| Licence | AGPL-3.0, Wings MIT | [Repository](https://github.com/pelican/panel) |
| Price | Free | [Comparison](https://pelican.dev/docs/comparison) |
| Stack | PHP 8.4 or 8.5, Laravel and Filament; Pelican Wings in Go | [Getting started](https://pelican.dev/docs/panel/getting-started) |
| To install | PHP 8.4+, Apache, NGINX, or Caddy, and SQLite by default (or MySQL, MariaDB, PostgreSQL), Redis optional; or Docker; a web installer | [Getting started](https://pelican.dev/docs/panel/getting-started) |
| Ports to open on a node | 8080 and 2022 | [Wings config](https://github.com/pelican/wings/blob/main/config/config.go) |
| Pterodactyl eggs | Imported, into its own YAML format; an egg browser | [Comparison](https://pelican.dev/docs/comparison) |
| Plugins and mods | Through the free official minecraft-modrinth plugin (and rust-umod); not in the core. No Steam Workshop | [Plugins](https://github.com/pelican/plugins) |
| Extensions | First-party plugins and an in-app Plugin Hub | [1.0.0-rc.3](https://github.com/pelican/panel/releases/tag/v1.0.0-rc.3) |
| Backups | Full archives to the machine or S3, several backup hosts per node; no encryption | [Backup adapters](https://github.com/pelican/panel/tree/main/app/Extensions/BackupAdapter/Schemas) |
| Databases | MySQL and MariaDB hosts, more planned; no browser | [Comparison](https://pelican.dev/docs/comparison) |
| Disk limits | Optional filesystem quotas, "still being worked on"; otherwise scanned | [Disk quotas](https://pelican.dev/docs/guides/disk-quotas/about) |
| Sign-in | Authenticator apps, email codes, passkeys, OAuth | [Comparison](https://pelican.dev/docs/comparison) |
| Alerts | HTTP webhooks, for the panel and per server; Discord moved to a plugin in 1.0.0-rc.1 | [1.0.0-rc.1](https://github.com/pelican/panel/releases/tag/v1.0.0-rc.1) |
| Isolation | Docker; Linux nodes | [Wings install](https://pelican.dev/docs/wings/install) |
| From Pterodactyl | Upgrades a Pterodactyl panel in place | [Migrating](https://pelican.dev/docs/guides/migrating-from-pterodactyl) |
| Latest release | 1.0.0-rc.3, November 14, 2026: a release candidate | [Releases](https://github.com/pelican/panel/releases) |
| GitHub stars | About 2,400 | [pelican/panel](https://github.com/pelican/panel) |

**Where it beats Raptor:** it's free, you host it, it has a plugin ecosystem and a large community, and it can run on SQLite with no database server.
**Where Raptor is ahead:** no panel to run or ports to open, add-ons and the Workshop built in, incremental encrypted backups, six database engines with a browser, kernel disk limits by default, and built-in alert channels.

## Pterodactyl

The original open-source game panel, which most of the others started from. 2.0 is in beta.

| | Pterodactyl | Source |
|---|---|---|
| What it is | The original open-source panel | [pterodactyl.io](https://pterodactyl.io) |
| Who runs the panel | You do | [Getting started](https://pterodactyl.io/panel/1.0/getting_started.html) |
| Licence | MIT | [LICENSE](https://github.com/pterodactyl/panel/blob/1.0-develop/LICENSE.md) |
| Price | Free; many features are paid add-ons from other authors | [Repository](https://github.com/pterodactyl/panel) |
| Stack | PHP (Laravel) and React; Wings in Go | [Wings](https://github.com/pterodactyl/wings) |
| To install | PHP 8.2 or 8.3, MySQL or MariaDB, Redis, a web server, Composer, a queue worker, and a cron job | [Getting started](https://pterodactyl.io/panel/1.0/getting_started.html) |
| Ports to open on a node | 8080 and 2022 | [Installing Wings](https://pterodactyl.io/wings/1.0/installing.html) |
| Pterodactyl eggs | Its own format | [pterodactyl.io](https://pterodactyl.io) |
| Plugins and mods | Paid add-ons (Blueprint, BuiltByBit, sourceXchange); no Steam Workshop in the panel | [Repository](https://github.com/pterodactyl/panel) |
| Extensions | None in 1.x (Blueprint is third-party); official extensions and themes in the 2.0 beta | [2.0.0-beta.1](https://github.com/pterodactyl/panel/releases/tag/v2.0.0-beta.1) |
| Backups | Full archives to the machine or S3; no encryption | [Changelog](https://github.com/pterodactyl/panel/blob/1.0-develop/CHANGELOG.md) |
| Databases | MySQL and MariaDB hosts; no browser | [pterodactyl.io](https://pterodactyl.io) |
| Disk limits | Scanned by Wings | [Wings](https://github.com/pterodactyl/wings) |
| Sign-in | Authenticator apps only; no passkeys, in 2.0 either | [2.0-develop](https://github.com/pterodactyl/panel/tree/2.0-develop) |
| Alerts | None found beyond install emails | [Changelog](https://github.com/pterodactyl/panel/blob/1.0-develop/CHANGELOG.md) |
| Isolation | Docker; Linux nodes | [Installing Wings](https://pterodactyl.io/wings/1.0/installing.html) |
| Latest release | 1.15.2, October 21, 2026; 2.0.0-beta.2, October 14 | [Releases](https://github.com/pterodactyl/panel/releases) |
| GitHub stars | About 9,500 | [pterodactyl/panel](https://github.com/pterodactyl/panel) |

The full comparison, with the add-ons Pterodactyl needs and their prices, is [Raptor vs Pterodactyl](/start/raptor-vs-pterodactyl/).

## PufferPanel

Its own panel in Go, not based on Pterodactyl: one program that's both the panel and the node.

| | PufferPanel | Source |
|---|---|---|
| What it is | An independent Go panel | [Repository](https://github.com/pufferpanel/pufferpanel) |
| Who runs the panel | You do | [Installing](https://docs.pufferpanel.com/en/latest/installing.html) |
| Licence | Apache-2.0 | [LICENSE](https://github.com/pufferpanel/pufferpanel/blob/v3/LICENSE) |
| Price | Free | [README](https://github.com/pufferpanel/pufferpanel#readme) |
| Stack | Go and Vue 3; one program is the panel and the node | [Repository](https://github.com/pufferpanel/pufferpanel) |
| To install | A deb or rpm package, or Docker; SQLite by default; no PHP or Redis | [Database guide](https://docs.pufferpanel.com/en/latest/guides/database.html) |
| Ports to open on a node | 8080 for the web and API, 5657 for SFTP | [Installing](https://docs.pufferpanel.com/en/latest/installing.html) |
| Pterodactyl eggs | No: its own templates | [Templates](https://docs.pufferpanel.com/en/latest/templates/templates.html) |
| Plugins and mods | CurseForge modpacks through templates; no plugin browser; SteamCMD downloads, no Workshop | [Operations](https://github.com/pufferpanel/pufferpanel/tree/v3/operations) |
| Extensions | Themes only | [Themes](https://docs.pufferpanel.com/en/latest/guides/themes/creating.html) |
| Backups | A full archive on the machine, with the server stopped; no S3, no encryption | [3.0.0 release notes](https://docs.pufferpanel.com/en/latest/release-notes/3.0.0.html) |
| Databases | None for game servers | [Docs](https://docs.pufferpanel.com) |
| Disk limits | None found | [Repository](https://github.com/pufferpanel/pufferpanel) |
| Sign-in | Authenticator apps and recovery codes; WebAuthn code appears in 3.0.9, unconfirmed in use | [login.go](https://github.com/pufferpanel/pufferpanel/blob/v3.0.9/web/auth/login.go) |
| Alerts | None found | [Repository](https://github.com/pufferpanel/pufferpanel) |
| Isolation | Docker is optional; by default servers run without isolation. Windows was dropped in 3.0 | [Standard environment](https://docs.pufferpanel.com/en/latest/environments/standard.html) |
| From Pterodactyl | No | [Docs](https://docs.pufferpanel.com) |
| Latest release | 3.0.9, July 14, 2026, stable | [Releases](https://github.com/pufferpanel/pufferpanel/releases) |
| GitHub stars | About 1,800 | [pufferpanel/pufferpanel](https://github.com/pufferpanel/pufferpanel) |

**Where it beats Raptor:** it's the simplest to install of the self-hosted panels (one package, SQLite), it's free, and it doesn't need Docker.
**Where Raptor is ahead:** nearly everything else here: isolation by default, Pterodactyl eggs, add-ons, backups that leave the machine, databases, disk limits, and alerts.

## Which one to pick

- **You'd rather not run a panel at all:** Raptor. Your machine runs the games and we run everything else, and your servers keep going if we don't.
- **You want to host your own, for free:** Calagopus is closest to Raptor in features. Pelican if you want plugins and a big community. Hydrodactyl if you're on Pterodactyl and want a modern fork you can switch to in place.
- **You also run VMs or web hosting:** FeatherPanel does those too.
- **You want the smallest install:** PufferPanel.
- **You need Windows nodes:** none of these run game servers on Windows.
