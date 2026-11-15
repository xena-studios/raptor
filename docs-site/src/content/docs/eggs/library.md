---
title: The egg library
description: What servers are made from, importing Pterodactyl eggs, Raptor's test server, and making your own.
sidebar:
  order: 1
---

An **egg** is the recipe for a kind of server: the Docker image it runs in, how to install it, how to start and stop it, the files it configures, and the settings people can change. Raptor reads **Pterodactyl eggs** as they are, so anything with one can run.

The org's **Eggs** page lists two kinds:

- **Raptor's:** the **Raptor test server**, tested with every release. It isn't a game: it answers in a browser on its port and says "ok" to `status` in the console, so it's the quickest way to check a new node installs, starts, and reaches a server.
- **Your org's own:** imported, made from scratch, or customised copies. Admins and owners change them; members see them.

## Using an egg

**Use egg** on its page opens Deploy server with it picked. Each egg's page shows what it runs: its runtimes, settings and who can change them, the startup command, the install script, and the config files it edits.

## Importing one

**Import eggs** on the Eggs page takes Raptor and Pterodactyl eggs (JSON or YAML), as many as you like: files you choose or drop, and links (a GitHub file page works). Before anything is saved, Raptor shows exactly what it would run: its Docker images and where they come from, its install script, its startup command, and its settings, with warnings for anything unusual, like an image from an unfamiliar publisher or a script that pipes a download into a shell. Each egg is listed with what it runs (open it to see the install script), its warnings, and its category. Untick any you don't want, tick that you've looked and trust them, then import them.

An imported egg is never marked certified, whatever it says. Most games have a community egg: search for the game's name and "Pterodactyl egg".

**Import new version** on one of your eggs replaces its file with a new one, from a link (its own source link is filled in) or an upload: an update upstream, or a change you made elsewhere. You review it the same way, and it's saved as a new version. Eggs from elsewhere don't have Raptor's settings (CPUs, players, add-ons), so the egg keeps its own unless you untick that.

## Customising

**Icons:** an egg shows the icon in its file, if it has one, or its category. Admins and owners can upload another on the egg's page (PNG, JPEG, GIF, or WebP, cropped square); removing it brings back the file's.

**Customize** makes a copy of any egg to edit, as a draft. The editor has tabs for its overview (name, category, and which CPUs it runs on), runtimes, variables, startup, install script, config files, and add-ons, plus the raw file. Saving writes a new **version**, so you can see who changed what. Eggs you make from scratch are in Raptor's own format; an edited Pterodactyl egg stays one. **Export** a Raptor egg as a Pterodactyl egg to use it on another panel.

- A **draft** can't be deployed; publish it when it's ready. An **archived** egg is hidden and can't be deployed.
- **Servers keep the egg they were made from.** Editing, archiving, or deleting an egg never changes a server by itself. To move a server to a newer version of its egg, use its [Egg version](/servers/settings/#egg-version); to move it to another egg, [change its type](/servers/settings/#change-the-servers-type).

## Accepting a game's EULA

Games like Minecraft need you to accept their EULA. Deploy server asks, and Raptor writes the acceptance for you.
