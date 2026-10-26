---
title: Questions
description: Short answers to what people ask most about Raptor.
sidebar:
  order: 5
---

## What does Raptor cost?

$12 a month for each machine you link, with every feature and unlimited servers. Your first machine is free for a month. See [Pricing and billing](/start/pricing/).

## Do I need to open ports?

Not for Raptor: your machine connects out to it. Players need each game's port open; the connection test on a server's **Network** tab tells you if they can't reach it. See [Players can't connect](/help/players-cant-connect/).

## Can I run it at home?

Yes, on any machine that meets [the requirements](/start/requirements/), including a box at home. Its `raptornodes.net` address follows your IP when it changes. Your router has to forward each game's port to the machine.

## What happens if Raptor goes down?

Your servers keep running, and schedules, backups, and crash restarts carry on. The `raptor` command on the machine still works. See [How Raptor works](/start/how-it-works/#if-raptor-goes-offline).

## How is Raptor different from Pterodactyl?

Pterodactyl's panel is yours to host, patch, and back up, and features like plugin installs, database browsing, and Discord alerts are paid add-ons. Raptor runs the panel for you, has those built in, and keeps your servers, schedules, and backups going when the panel is offline. See [Raptor vs Pterodactyl](/start/raptor-vs-pterodactyl/), including where Pterodactyl is the better choice.

## Can I run Raptor next to Pterodactyl?

Yes, on the same machine, and move servers over when you're ready with `raptor import pterodactyl`. See [Move from Pterodactyl](/install/from-pterodactyl/).

## Which games can I run?

Anything with a Pterodactyl or Pelican egg. Raptor's catalog has Minecraft (Paper, Fabric, Forge, and more), Rust, Valheim, Palworld, ARK, Terraria, Factorio, and many others, plus Discord bots and voice servers. See [The egg library](/eggs/library/).

## Can my friends help run the servers?

Invite them to your org, and give each exactly what they need on each server. See [Members and access](/team/members/).

## Why does Raptor need my passkey?

So that deleting servers, restoring backups, and changing what runs can't happen without you, even if someone got into Raptor itself. See [How Raptor protects you](/security/how-raptor-protects-you/).

## Where are my files and backups?

On your machine. Backups go where you choose, encrypted on your machine first. See [Backups](/servers/backups/).

## How do I leave Raptor?

First remove the node on its **Settings** tab, so it stops being billed. Its servers keep running on the machine. Then, on the machine:

```bash
sudo raptor uninstall
```

It stops every server gracefully, then removes Raptor's services, containers, networks, firewall rules, and the `raptor` command. Docker stays installed. Your servers' files, the backups kept on the machine, and your databases stay in `/var/lib/raptor`: the command says how to get at the files, which are inside a disk image unless you gave Raptor a disk of its own. Raptor's firewall goes too, so if you used it to close ports, set up your own.

To delete all of that as well, use `sudo raptor uninstall -wipe-data`. It asks you to type `delete` first. Backups stored elsewhere, like Raptor Backup Storage or your own S3, aren't touched by either.

If a step fails, the command says why and keeps itself, so you can fix it and run it again.

## Is Raptor open source?

Yes, all of it, under the AGPL: [github.com/xena-studios/raptor](https://github.com/xena-studios/raptor).
