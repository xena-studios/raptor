---
title: The egg library
description: What servers are made from, Raptor's catalog, importing Pterodactyl and Pelican eggs, and making your own.
sidebar:
  order: 1
---

An **egg** is the recipe for a kind of server: the Docker image it runs in, how to install it, how to start and stop it, the files it configures, and the settings people can change. Raptor reads **Pterodactyl and Pelican eggs** as they are, so anything with one can run.

The org's **Eggs** page lists two kinds:

- **Raptor's:** the catalog, maintained by Raptor. **Certified** ones (with a check) are tested with every release: Minecraft (Paper, Purpur, Fabric, Forge, NeoForge, Quilt, Vanilla, Bedrock), proxies (Velocity, BungeeCord), Steam games (Rust, Valheim, Palworld, ARK, CS2, Satisfactory…), Terraria, Factorio, and more.
- **Your org's own:** imported, made from scratch, or customized copies. Admins and owners change them; members see them.

## Using an egg

**Use egg** on its page opens Deploy server with it picked. Each egg's page shows what it runs: its runtimes, settings and who can change them, the startup command, the install script, and the config files it edits.

## Importing one

**Import an egg** on the Eggs page takes a Pterodactyl or Pelican egg (JSON or YAML), uploaded or from a link (a GitHub file page works). Before anything is saved, Raptor shows exactly what it would run: its Docker images and where they come from, its install script, its startup command, and its settings, with warnings for anything unusual, like an image from an unfamiliar publisher or a script that pipes a download into a shell. Tick that you've looked and trust it, then import it.

An imported egg is never marked certified, whatever it says. [Community eggs](https://github.com/pelican-eggs) are a good place to find more.

**Import new version** on one of your eggs replaces its file with a new one, from a link (its own source link is filled in) or an upload: an update upstream, or a change you made elsewhere. You review it the same way, and it's saved as a new version. Files from Pterodactyl and Pelican don't have Raptor's settings (CPUs, players, add-ons), so the egg keeps its own unless you untick that.

## Customizing

**Customize** makes a copy of any egg to edit, as a draft. The editor has tabs for its overview (name, category, and which CPUs it runs on), runtimes, variables, startup, install script, config files, and add-ons, plus the raw file. Saving writes a new **version**, so you can see who changed what. An edited egg is still a valid Pterodactyl egg: **Export** it to use anywhere.

- A **draft** can't be deployed; publish it when it's ready. An **archived** egg is hidden and can't be deployed.
- **Servers keep the egg they were made from.** Editing, archiving, or deleting an egg never changes a running server; [change a server's type](/servers/settings/#change-the-servers-type) to move it to another egg.

## Accepting a game's EULA

Games like Minecraft need you to accept their EULA. Deploy server asks, and Raptor writes the acceptance for you.
