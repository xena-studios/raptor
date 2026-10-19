---
title: Move from Pterodactyl
description: Bring your Pterodactyl servers over to Raptor on the same machine, files and all, without touching the originals.
sidebar:
  order: 3
---

Raptor and Pterodactyl's Wings can run side by side on one machine, so you can move servers over one at a time, at your own pace, and keep the way back open.

## What moves

`raptor import pterodactyl` copies each server you pick into Raptor:

- **Its egg**, rebuilt from your Pterodactyl Panel (with anything it inherits from a parent egg), so it runs exactly as before.
- **Its settings:** Docker image, startup command, variables, memory, CPU and disk limits, and ports.
- **Its files**, copied into Raptor's server data volume. Links stay links; everything is owned by Raptor's server user.

It's created as already installed: the install script doesn't run again.

## What's left alone

**Pterodactyl's files and config are never changed or deleted.** Copying rather than moving keeps the way back open, at the cost of the disk space until you remove the originals yourself. The server is **suspended** in Pterodactyl afterwards, so it isn't started there by mistake; unsuspending it in Pterodactyl undoes that.

## Before you start

1. [Connect the machine](/install/first-node/) to Raptor, with Pterodactyl's Wings still running on it. The installer keeps Raptor's networks, ports, and containers apart from Pterodactyl's.
2. In your Pterodactyl Panel, make an **application API key** (Admin → Application API). The importer uses it to read this node, its servers, and their eggs, and to suspend each server it moves, so give it read and write access to those.
3. Make sure the machine has room for a copy of each server you're moving.

## Move your servers

See what would move, without changing anything:

```bash
sudo raptor import pterodactyl -key ptla_… -dry-run
```

Move everything on this machine:

```bash
sudo raptor import pterodactyl -key ptla_…
```

Or one server, by its UUID, short ID, or name, and start it in Raptor straight away:

```bash
sudo raptor import pterodactyl -key ptla_… -server survival -start
```

For each server it stops it in Pterodactyl (waiting up to 2 minutes for it to shut down cleanly), copies it in, and suspends it in Pterodactyl. If a copy fails, the half-made server is removed and Pterodactyl's is left as it was.

:::tip[Keep the key out of your shell history]
Put it in `RAPTOR_PTERODACTYL_KEY` instead of `-key`. Raptor never stores it.
:::

| Flag | What it does |
|---|---|
| `-key` | The application API key (or `$RAPTOR_PTERODACTYL_KEY`) |
| `-server` | Only this server: its UUID, short ID, or name |
| `-dry-run` | List what would move, and stop |
| `-start` | Start each server in Raptor once it's copied |
| `-no-suspend` | Leave the server unsuspended in Pterodactyl |

## Afterwards

- The servers show up in the app, on that node, as they were. Players connect to the same ports.
- Check each one runs, then turn off Pterodactyl's Wings when you're done with it: the import says how at the end.
- Remove the old copies from Pterodactyl's data directory (usually `/var/lib/pterodactyl/volumes`) once you're sure.
- A `.pteroignore` file in a server keeps working: Raptor's backups leave out what it lists.

## Moving servers from another machine

The importer works on the machine both run on. For a server on a different machine, or at a game host:

- If it offers SFTP, use **Import** in the server's file manager: Raptor copies its files in over SFTP. See [Files](/servers/files/#import-from-another-host).
- Or upload an archive of it, and extract it in the file manager.

Make the server in Raptor first, with the same game, then bring its files in.
