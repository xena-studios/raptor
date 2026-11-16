---
title: The egg library
description: What servers are made from, Raptor's eggs for the games and apps people host, importing Pterodactyl eggs, and making your own.
sidebar:
  order: 1
---

An **egg** is the recipe for a kind of server: the Docker image it runs in, how to install it, how to start and stop it, the files it configures, and the settings people can change. Raptor reads **Pterodactyl eggs** as they are, so anything with one can run.

The org's **Eggs** page lists two kinds:

- **Raptor's:** eggs for the games and apps people host (Minecraft and its server software and modpacks, Steam games, voice servers, apps, and plain language runtimes), made and tested by Raptor on real nodes and kept up to date. When Raptor updates one, its page says so, and servers move to the new version when you choose.
- **Your org's own:** imported, made from scratch, or customised copies. Admins and owners change them; members see them.

**Sort** sets the order: **Popular** (the start: what people host most, so a new Panel still opens on Paper, Fabric, Rust, and the like), **Most servers** made from each, **Newest** (recently added or updated), or **Name**; community eggs can also go by **Most copied**. Popular counts servers that are running and ones made in the last month for more, so it follows what people host now. The few most-used eggs in each category get a **Popular** badge, and those picked most lately a **Trending** one. Your choice is remembered in this browser and kept in the page's link (`?sort=`), so a shared link keeps it. Your org's eggs stay above Raptor's, and drafts and archived eggs stay last, whatever the order. Picking a game for a new server lists them by Popular too.

## Using an egg

**Use egg** on its page opens Deploy server with it picked. Modpack eggs (Modrinth, FTB, and Technic) have **Browse modpacks** there, to pick the pack and its version instead of typing their IDs. Each egg's page shows what it runs: its runtimes, settings and who can change them, the startup command, the install script, and the config files it edits.

## Importing one

**Import eggs** on the Eggs page takes Raptor and Pterodactyl eggs (JSON or YAML), as many as you like: files you choose or drop, and links (a GitHub file page works). Before anything is saved, Raptor shows exactly what it would run: its Docker images and where they come from, its install script, its startup command, and its settings, with warnings for anything unusual, like an image from an unfamiliar publisher or a script that pipes a download into a shell. Each egg is listed with what it runs (open it to see the install script), its warnings, and its category. Untick any you don't want, tick that you've looked and trust them, then import them.

An imported egg is never marked certified, whatever it says. For a game Raptor doesn't have, there's usually a community egg: search for the game's name and "Pterodactyl egg".

**Import new version** on one of your eggs replaces its file with a new one, from a link (its own source link is filled in) or an upload: an update upstream, or a change you made elsewhere. You review it the same way, and it's saved as a new version. Eggs from elsewhere don't have Raptor's settings (CPUs, players, add-ons), so the egg keeps its own unless you untick that.

## Customising

**Icons:** an egg shows the icon in its file, if it has one, or its category. Admins and owners can upload another on the egg's page (PNG, JPEG, GIF, or WebP, cropped square); removing it brings back the file's.

**Customize** makes a copy of any egg to edit, as a draft (on your org's own eggs it's **Duplicate**; **Edit** changes the egg itself). The editor has tabs for its overview (name, category, and which CPUs it runs on), runtimes, variables, startup, install script, config files, and add-ons, plus the raw file. Saving writes a new **version**, so you can see who changed what. Eggs you make from scratch are in Raptor's own format; an edited Pterodactyl egg stays one. **Export** a Raptor egg as a Pterodactyl egg to use it on another panel.

- A **draft** can't be deployed; publish it when it's ready. An **archived** egg is hidden and can't be deployed.
- **Servers keep the egg they were made from.** Editing, archiving, or deleting an egg never changes a server by itself. To move a server to a newer version of its egg, use its [Egg version](/servers/settings/#egg-version); to move it to another egg, [change its type](/servers/settings/#change-the-servers-type).

## Accepting a game's EULA

Games like Minecraft need you to accept their EULA. Deploy server asks, and Raptor writes the acceptance for you.
