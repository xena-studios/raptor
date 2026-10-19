---
title: Backups
description: Automatic backups, where they go, how many are kept, restoring all or part of a server, backup keys, and Raptor Backup Storage.
sidebar:
  order: 4
---

Every new server backs itself up once a day, with nothing to set up. Backups are **incremental** (only what changed is stored), **deduplicated** (a file several servers share is stored once), **compressed**, and **encrypted on your machine** before they go anywhere.

## Back up now

**Back up now** on the server's **Backups** tab takes one at once (the **Backups** permission). Its progress shows in the list. For Minecraft and other games that support it, Raptor tells the game to save and pause writing first (`save-off`, `save-all`), then resumes it after, so the world is backed up whole.

Each backup in the list shows when it was taken, why (manual, scheduled, a safety backup), where it is, and its size. From its menu you can **lock** it (retention never deletes a locked backup) or delete it.

## The daily backup

New servers get a **Daily backup** schedule, sometime between 4 and 5 AM in the node's time zone. It's an ordinary schedule on the [Schedules](/servers/schedules/) tab: move it, change it, or turn it off. It's skipped while the server is offline, since nothing has changed.

## Restore

Choose **Restore** on a backup to open it, read-only, like the file manager.

- **Restore a few files:** pick files or folders and restore them. They're copied into a new `.restore/<when the backup was taken>` folder beside the server's files, so nothing is replaced, and the server can keep running. Move what you need into place from there.
- **Restore everything:** puts the whole server back as it was: every file replaced, anything made since deleted. The server stops, a **safety backup** of what it has now is taken first (kept 7 days), and it starts again after. Signed with your passkey.

## Databases in backups

A backup also holds the server's [databases](/servers/databases/): just before it's taken, each one is dumped, and the list of backups says which databases each holds. The dumps don't use the server's disk, and the patterns for leaving files out don't apply to them.

**Restore everything** puts the databases back too, before the server starts. To put back only a database, without touching the files, use **Restore from a backup** on the server's [Databases tab](/servers/databases/#restore-a-database-from-a-backup).

A database whose instance is stopped can't be dumped: the backup is taken without it, with a warning saying so.

## Where backups go

**Where backups go** on the Backups tab lists the destinations this server backs up to, each with how many backups it keeps. A server can back up to up to five destinations at once; each backup is taken to every one. Admins change them; sending backups somewhere new, or keeping fewer, is signed with a passkey.

Destinations are set up per node, on the **node's Backups tab** (admins):

| Destination | For |
|---|---|
| **Raptor Backup Storage** | Offsite, with nothing to set up. [Below](#raptor-backup-storage). |
| **This node's disk** | Always there. Fast to restore, but gone if the machine is. |
| **A folder on the node** | A second disk or a mounted NAS, under `/mnt`, `/media`, or `/srv`. |
| **S3-compatible** | AWS, Backblaze B2, Cloudflare R2, Wasabi, Hetzner, OVH, Google Cloud Storage, MinIO… |
| **Azure Blob Storage** | With an account key or a SAS token. |
| **SFTP** | Any server over SSH. Raptor shows the server's host key to check, and can sign in with the node's own key, a password, or a private key. |
| **WebDAV** | Nextcloud, ownCloud, a NAS. |

**Test** a destination before saving it: the node writes a file there, reads it back, and removes it, and says in plain words what's wrong if anything is. Each one can limit its **upload speed**, so backups leave room for players. Its health (when it last worked, or why it's failing) shows on the node's Backups tab, and **Settings → Backups** shows every node's at once.

## How many are kept

Each destination keeps backups by a rule:

| Preset | Keeps |
|---|---|
| **Light** | The last 3 |
| **Standard** | The last 3, a week of dailies, and a month of weeklies (the default) |
| **Long-term** | Standard, plus one a month for 6 months |
| **Custom** | Your own numbers of recent, daily, weekly, and monthly backups |

"7 daily" means the newest backup from each of the last 7 days that had one, so a server that was off for a week doesn't lose its history to the calendar. A backup any part of the rule keeps is kept. **Locked** backups, safety backups, and final backups never count.

Space from deleted backups is freed a day or two later, when the destination is cleaned up.

## Leaving files out

Add patterns (like a `.gitignore`) to leave files out of a server's backups: caches, logs, or a dynmap render. A `.pteroignore` file in the server works too. The server's databases are always included.

## Raptor Backup Storage

Offsite backups in Raptor's storage, with no account or keys to set up. Turn it on for a node on its **Backups** tab → **Add destination**, then add it to servers.

- **10 GB included** per node on a plan, shared across the org; more is $12 per TB a month. See [Pricing](/start/pricing/).
- **Encrypted on your node** before it leaves. Raptor stores data it can't read without your node's backup key ([below](#the-backup-key)).
- **A hacked node can't destroy them:** its key can't delete for good. Anything deleted is kept, hidden, for 30 days, and can be brought back.
- Turn it off and the files are kept 30 days, then deleted.

## The backup key

Each node encrypts its backups with its own **backup key**. Without it, nobody can read them, including you.

By default **Raptor keeps a copy**, sealed, so if the machine dies you can recover its backups on another one. Prefer that Raptor couldn't read them? On the node's **Backups** tab → **Backup key**:

- **Download the key** (with an owner's passkey) and keep it somewhere safe, or show it on the machine with `sudo raptor backup key`, so it never passes through Raptor.
- **Keep it yourself:** Raptor deletes its copy. Lose yours too, and a dead machine's backups are gone for good.

## Recovering a lost machine's backups

On another node's **Backups** tab, owners can **Recover backups** from a node that's gone (or one you removed): from its Raptor Backup Storage, or from its own destination with its backup key. Its servers' backups appear under **Recovered backups**, to restore onto a server here with your passkey.

The same screen can **look back in time** on S3 or Raptor Backup Storage, to find backups deleted since: by retention, or by someone who got into the machine.

## On the machine

```bash
sudo raptor backup list survival      # a server's backups
sudo raptor backup create survival    # back up now, and wait for it
sudo raptor backup restore survival <id>
```

`restore` asks for the server's name before replacing anything, and takes a safety backup first.
