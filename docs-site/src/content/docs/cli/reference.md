---
title: The raptor command
description: Every raptor command on the machine, for keeping servers running when you're on the box or Raptor is offline.
sidebar:
  order: 1
---

Every node has the `raptor` command. It's for **keeping things running**: seeing what's up, power, the console, logs, backups, and fixing problems. It never changes servers' settings; that's the app's job.

Commands that change something need `sudo`. Members of the `raptor` group can look (status, `ps`, logs) without it.

`<server>` is a server's ID, its short ID from `raptor ps`, or its exact name.

## Seeing what's going on

| Command | Shows |
|---|---|
| `raptor status` | The node's health, its link to the Panel, Docker, storage, and server counts |
| `raptor ps` | Every server: short ID, state, CPU, memory, disk, address, uptime. `-json` for scripts |
| `raptor logs <server>` | A server's output, further back than the console. `-n 500` lines, `-f` to follow, `-t` with timestamps, `-n -1` for everything |
| `raptor tui` | A full-screen view: servers, stats, history graphs, the console, and keys for power, backups, and commands |
| `raptor audit` | Signed actions and key resets, from the node's own records. `-n 50`, `-since 72h` |
| `raptor version` | The Wings version |

## Running servers

| Command | Does |
|---|---|
| `sudo raptor start <server>` | Start it |
| `sudo raptor stop <server>` | Stop it cleanly, waiting for it to save |
| `sudo raptor restart <server>` | Stop, then start |
| `sudo raptor kill <server>` | Stop it at once, without saving |
| `sudo raptor console <server>` | The live console. Type commands to send them; Ctrl-C detaches and leaves it running |

Send one command from a script:

```bash
echo "say Back in five" | sudo raptor console survival
```

Power actions and commands from here are recorded in the server's activity, as done by your Unix user.

## Backups

| Command | Does |
|---|---|
| `raptor backup list [<server>]` | A server's backups, or every server's |
| `sudo raptor backup create <server>` | Back up now, and wait. `-lock` keeps it from retention |
| `sudo raptor backup restore <server> <id>` | Restore one. Asks for the server's name first (or `-yes`), and takes a safety backup |
| `sudo raptor backup key` | The node's backup key, to save it without it passing through Raptor |

## Fixing problems

| Command | Does |
|---|---|
| `sudo raptor doctor` | Checks everything and says how to fix what's wrong. See [raptor doctor](/cli/doctor/) |
| `sudo raptor doctor -upload` | The same, and sends a redacted diagnostics file to support, for a support code |
| `sudo raptor update` | Update Wings now. `-check` to see what it would install; `-version` for one in particular. See [Updates](/nodes/updates/) |
| `sudo raptor keys list` | The passkeys this node trusts for signed actions |
| `sudo raptor keys reset` | Re-pair your passkey from the machine, if you lost the ones it trusts |

## Storage

| Command | Does |
|---|---|
| `raptor storage status` | The server data volume: its kind, size, and how full it is |
| `sudo raptor storage setup` | Create the data volume, if the installer didn't |
| `sudo raptor storage grow -size 200GiB` | Grow an image volume, while servers run |
| `raptor storage disks` | The machine's disks, and what each is used for |
| `sudo raptor storage add-disk -device /dev/sdb` | Format an empty disk and use it for servers (or `-role backups`). The node's **Disks** tab does the same |
| `sudo raptor storage grow-disk -id <id>` | Grow a data disk's filesystem after you made the disk bigger, while servers run |

## Linking

| Command | Does |
|---|---|
| `sudo raptor bootstrap -token rpt_join_…` | Set up a fresh machine and link it. What the install command runs |
| `sudo raptor link -token rpt_join_…` | Link an already set-up machine with a join token |
| `sudo raptor relink -token rpt_join_…` | Link again as the same node, after it was removed or its key revoked |
| `sudo raptor unlink` | Stop connecting to the Panel. Servers keep running |
| `sudo raptor uninstall [-wipe-data]` | Take Raptor off the machine. Server files, backups, and databases stay unless `-wipe-data` ([Leaving Raptor](/help/faq/#how-do-i-leave-raptor)) |

## Support and moving in

| Command | Does |
|---|---|
| `raptor support status` | Whether Raptor's staff have access to this node, and until when |
| `sudo raptor support revoke <id>\|all` | End their access now |
| `sudo raptor support off` / `sudo raptor support on` | Refuse all support access, or allow requests again |
| `sudo raptor import pterodactyl -key ptla_…` | Move servers from Pterodactyl on this machine. See [Move from Pterodactyl](/install/from-pterodactyl/) |

Run `raptor <command> -h` for every flag a command takes.
