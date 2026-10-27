---
title: Raptor vs Pterodactyl
description: What you run, what you get, and what you pay with Raptor and with Pterodactyl, feature by feature, with the add-ons Pterodactyl needs and their prices.
sidebar:
  order: 4
---

Raptor does the job Pterodactyl does: it runs game servers on your machines in Docker, from eggs, managed in your browser. The difference is who runs the panel. With Pterodactyl, you do. With Raptor, we do, and your machine only runs your games.

This page compares the two honestly, including where Pterodactyl is the better choice. Prices and features are as of October 2026.

## What you run

Pterodactyl is two programs: the **Panel**, a PHP web app, and **Wings**, on each machine. Before your first server, you install and keep running:

| | Pterodactyl | Raptor |
|---|---|---|
| A machine for the panel | A VPS, or a share of your game machine's memory | Not needed: we run it at app.raptorpanel.net |
| Web stack | nginx (or Apache), PHP, Composer | None |
| Database and cache | MariaDB or MySQL, and Redis, which you back up | None |
| Background work | A queue worker and a cron job; without them schedules don't run | None |
| Domain and DNS | For the panel and every node | Every node gets a hostname that follows its IP |
| Certificates | For the panel, and again for every node | None |
| Ports on each node | 8080 for the panel to reach Wings, 2022 for SFTP | None for Raptor: the node connects out. SFTP only if you turn it on |
| Updates | By hand: back up, put the panel in maintenance, pull, migrate, re-apply add-ons | Wings updates itself and rolls back if an update fails. See [Updates](/nodes/updates/) |
| Linking a node | Make it in the panel, copy its config file, set up its certificate, start Wings | One command with a one-time token. See [Connect your first node](/install/first-node/) |

## Feature by feature

**Built in** means it comes with the product. **Add-on** means someone sells one for Pterodactyl, priced below. **None** means there's no add-on we could find.

| Feature | Pterodactyl | Raptor |
|---|---|---|
| Console, power, stats, file manager, SFTP, schedules | Built in | Built in |
| Pterodactyl and Pelican eggs | Built in | Built in, and [your servers move over](/install/from-pterodactyl/) |
| Sub-users with per-server permissions, activity log | Built in | Built in |
| MySQL databases for servers | Built in (MySQL and MariaDB) | Built in: MariaDB, MySQL, PostgreSQL, Redis, Valkey, and MongoDB. See [Databases](/nodes/databases/) |
| Install plugins (Modrinth, Hangar) | Add-on | Built in. See [Plugins, mods, and the Workshop](/servers/addons/) |
| Install mods | Add-on | Built in |
| Steam Workshop items | None in general; add-ons for single games | Built in, for Garry's Mod, ARK, and Squad |
| Change a server's type (egg) | Add-on | Built in. See [Server settings](/servers/settings/#change-the-servers-type) |
| Import a server from another host over SFTP | Add-on | Built in. See [Files](/servers/files/#import-from-another-host) |
| Browse and edit a database | Add-on (phpMyAdmin) | Built in, for every engine, with read-only users |
| Databases in backups | Add-on | Built in. See [Backups](/servers/backups/#databases-in-backups) |
| Alerts | Add-on (Discord only) | Built in: email, Discord, Slack, Telegram, ntfy, or a webhook. See [Alerts](/team/alerts/) |
| Backups | Full archives, restored whole | Incremental, deduplicated, encrypted, and restorable a file at a time. See [Backups](/servers/backups/) |
| Offsite backups | Your own S3, which you set up | Your own S3, B2, or SFTP, or [Raptor Backup Storage](/servers/backups/#raptor-backup-storage) with nothing to set up |
| A safety backup before a wipe or restore | None | Built in |
| A trash for deleted files | None | Built in. See [The trash](/servers/files/#the-trash) |
| Disk limits | Checked by scanning, so a server can go over before it's caught | Enforced by the kernel as files are written |
| A command line on the machine | Wings' own diagnostics only | `raptor`: status, power, console, logs, backups, and a terminal UI. See [The raptor command](/cli/reference/) |
| Diagnosing problems | Logs | [`raptor doctor`](/cli/doctor/) says what's wrong and how to fix it |
| "Players can't connect" | You work it out | A connection test, with steps for your provider's firewall. See [Players can't connect](/help/players-cant-connect/) |
| Private networks between servers | None | Built in, with rules between networks. See [Networks](/nodes/networks/) |
| If the panel goes down | Nothing can be managed and schedules stop | Servers, schedules, backups, and crash restarts carry on, and the `raptor` command still works |
| Deleting a server, restoring a backup, changing what runs | Anyone who gets into the panel | Only with your passkey, checked by your machine. See [How Raptor protects you](/security/how-raptor-protects-you/) |
| Help when something breaks | Volunteers on forums and Discord | The Raptor team, in a private ticket. See [Get help](/help/get-help/) |
| Help moving from Pterodactyl | None | The importer, and the team if you get stuck. See [Move from Pterodactyl](/install/from-pterodactyl/) |
| Support access to your machine | Whatever you hand over, often root | Only when you approve it, for as long as you say, recorded, and never a shell. See [Support access](/nodes/overview/#support-access) |

## What it costs

Pterodactyl's code is free. Making it do what Raptor does means paying for add-ons and for somewhere to run the panel. For the add-ons, we used a current listing for each feature, licensed for one panel. Some have cheaper look-alikes and some cost more; check the listing before you buy, since prices change.

| Feature | Add-on | Price, once |
|---|---|---|
| Plugin installer | [Minecraft Plugin Installer](https://builtbybit.com/resources/pterodactyl-minecraft-plugin-installer.18332/) | $17.50 |
| Mod installer | [Modpack & Mods Installer](https://builtbybit.com/resources/modpack-mods-installer.96885/) | $5.99 |
| Change a server's type | [Egg Changer](https://blueprint.zip/browse/eggchanger) | $6.49 |
| Import from another host | [Server Importer](https://builtbybit.com/resources/addon-pterodactyl-server-importer.79453/) | $7.99 |
| Browse and edit databases | [Automatic phpMyAdmin](https://builtbybit.com/resources/automatic-phpmyadmin-for-pterodactyl.24760/) | $16.99 |
| Databases in backups | [MySQL Backup Manager](https://pterodactyl-extensions.com/product/pterodactyl-mysql-backup-manager) | $14.49 |
| Alerts on Discord | [Discord Notifications](https://builtbybit.com/resources/discord-notifications-for-pterodactyl.50943/) | $5.00 |
| **Add-ons** | | **$74.45** |

Some add-ons need [Blueprint](https://blueprint.zip), a free framework for Pterodactyl extensions. Many have to be applied again after a panel update, and each is supported by a different author.

Running the panel: a small VPS is about **$6 a month** ($72 a year), and a domain about **$12 a year**.

Someone has to install it. If that isn't you, the cheapest install we found for hire was [$20 on BuiltByBit](https://builtbybit.com/threads/20-setup-%E2%9A%A1-pterodactyl-game-panel-installation-%E2%9A%A1.678903/) (the panel, one node, and certificates), and [Fiverr gigs](https://fiverr.com/garciaqjesus/install-pterodactyl-panel-and-wings) charge up to $50. Keeping it patched afterwards is ongoing work nobody lists a price for; we estimate **$20 to $35 a month** for someone to do it, and count it at $20. With Raptor, there's nothing to install but the one command, and the team helps when the machine itself is the problem.

### One machine

| | Pterodactyl | Raptor |
|---|---|---|
| Year one, installing it yourself | $74.45 + $72 + $12 = **$158.45** | First month free, then 11 × $12 = **$132** |
| Year one, with a hired install | $158.45 + $20 = **$178.45** | **$132**, and the team's help is included |
| Year one, with a sysadmin (install, then $20 a month) | $178.45 + $240 = **$418.45** | **$132** |
| Each year after, yourself | $72 + $12 = **$84** | 12 × $12 = **$144** |
| Each year after, with a sysadmin | $84 + $240 = **$324** | **$144** |
| Panel on your game machine instead of a VPS | **$86.45** in year one, **$12** a year after, and the panel uses the machine's memory | Not needed |

In year one, Raptor costs less either way. After that, Pterodactyl costs less in money if you keep it running yourself, and more if you pay someone to. What Raptor saves you is the panel (its updates, its security, its backups, and the evenings they take) and a sysadmin to call when you're stuck.

### More machines

Pterodactyl's panel costs the same however many machines it manages. Raptor is $12 a month for each one. With several machines, Pterodactyl costs less in money, and you're running a panel for all of them.

## Where Pterodactyl is the better choice

- **You're a hosting company.** Pterodactyl works with billing systems (WHMCS, Paymenter, Blesta) and can be white-labelled. Raptor has no reseller features, and none are planned.
- **You want everything on your own hardware**, including the panel, or a network that can't reach the internet.
- **You run many machines** and the cost per machine matters more than your time.
- **You want a theme or an add-on Raptor doesn't have.** Pterodactyl has years of community add-ons.

## Moving over

Raptor runs alongside Pterodactyl on the same machine, so you don't have to switch all at once. `raptor import pterodactyl` copies each server over (its egg, settings, and files) and leaves Pterodactyl's copy untouched, suspended, until you remove it. See [Move from Pterodactyl](/install/from-pterodactyl/).

Your first machine is free for a month. See [Pricing](/start/pricing/).
