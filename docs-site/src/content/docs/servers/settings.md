---
title: Server settings
description: Limits, crash and stop behaviour, startup and game settings, ports, changing the game, reinstalling, moving, and deleting.
sidebar:
  order: 2
---

A server's settings are spread over a few tabs: **Settings** for its name, limits, and behaviour; **Startup** for its runtime and game settings; and **Network** for its ports and address.

Most changes take effect the **next time the server starts**; the app says when one needs a restart. Changes that alter what code runs (the runtime, the startup command, the game) are signed with your passkey.

## Name and limits

On **Settings**:

| Setting | What it does |
|---|---|
| **Name** | What it's called in Raptor. Up to 64 characters. |
| **Memory** | The most memory the game may use. Too little and it crashes with "out of memory"; Minecraft wants 2–4 GB for a few friends, more with mods. |
| **Disk limit** | The most space its files may take, including its trash. Empty means no limit. Applies at once. |
| **Swap** | Disk the game may use as extra memory once its memory is full. Much slower than memory; most games run better without it. |
| **CPU limit** | The most CPU it may use, in cores (0.5 is half a core, 2 is two). Without one, every server shares the machine's CPU fairly, which is usually best. |

## Crashes and stopping

| Setting | What it does |
|---|---|
| **Restart it when it crashes** | On by default. Raptor starts it again after a crash, and gives up after three in ten minutes ([how](/servers/overview/#crashes)). Off, a crashed server stays down until someone starts it. |
| **A clean exit means it stopped** | Off by default. When the game quits without an error, as after `/stop` in its own console, it's left stopped instead of counted as a crash and restarted. |
| **Force stop after** | How long **Stop** waits for the game to save and shut down before it's killed: 1 to 600 seconds, 60 by default. Big worlds can need longer. |

These apply at once, even while the server runs.

## Startup and game settings

On **Startup**:

- **Startup command:** what the server runs, built from its game's recipe (its [egg](/eggs/library/)).
- **Runtime:** for games with more than one, such as Java versions for Minecraft. Changing it is signed with your passkey.
- **Game settings:** versions, jar names, world names, passwords, and the like, each with what it does. Some are fixed by the game's recipe and shown greyed out; members only see the ones they're allowed to.

Games with Steam Workshop support get a **Workshop** tab, and Minecraft servers a **Plugins**, **Mods**, or **Datapacks** tab: see [Plugins, mods, and the Workshop](/servers/addons/).

## Ports and address

On **Network**:

- **Port:** the port players connect to, 1024–65535, not used by another server on the node. It changes at the next start.
- **Shown address:** what players should connect to, if not the node's own address: a domain (`play.example.com`), a domain with an SRV record, or a proxy like TCPShield. It's shown exactly as you type it, so add the port if players need one.
- **Networks** (admins and owners, on nodes with [networks](/nodes/networks/)): which network the server is on, and others it also joins, like a proxy. It changes at the next start.
- **Connection test:** checks from outside whether players can reach the server, and says what to change if they can't. See [Players can't connect](/help/players-cant-connect/).

## Change the server's type

**Server type** on **Settings** (admins and owners) changes what the server runs: Paper to Purpur, Forge to NeoForge, or another game entirely. Pick the new type, then what happens to the files:

- **Keep them and reinstall:** after a backup you can restore from, the new install runs over the files the server has, so worlds and configs stay. Best between versions of the same game.
- **Start fresh:** every file is deleted, after a backup you can restore from, and the new type installs clean.

Settings both types share (a Minecraft version, say) keep their values; the rest start at the new type's defaults. The server must be stopped, and your passkey signs the change.

## Reinstall

**Reinstall** on **Settings** runs the install script again, for a broken install or to pick up a change to its game settings.

- **Reinstall:** a backup is taken, then the install runs over what's there. Usually what you want.
- **Wipe and reinstall:** a backup is taken, every file is deleted, then it installs clean. Signed with your passkey.

Either way, if the backup fails, nothing is changed and the console says why. A server with no files yet needs no backup. These backups are listed as safety backups on **Backups** and kept for 7 days.

The server must be stopped, and you type its name to confirm.

## Details

**Details** on **Settings** shows the server's ID, for support and the [raptor command](/cli/reference/), with its node and game.

## Move to another disk

When the node has [more than one disk](/nodes/disks/) for servers, the **Disk** card on **Settings** (admins) moves the server's files to another. It stops the server, copies everything, switches over, and only then removes the old copy, so nothing is lost if it's interrupted. It starts again afterwards if it was running.

## Delete

**Delete server** (admins and owners) is permanent. Type its name and sign with your passkey.

- **Take a final backup** (on by default when it has somewhere to back up to): the server is backed up first, and if that fails, it isn't deleted. A final backup on the node itself is kept 30 days; offsite ones are kept like any other.
- Its files, local backups, schedules, and ports go with it.
- A deleted server's offsite backups stay in the Panel, to restore onto another server with your passkey.
