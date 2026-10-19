---
title: Disks
description: The server data volume, adding a second disk for servers or backups, and growing disks.
sidebar:
  order: 2
---

Servers' files live on the **server data volume**, which enforces each server's disk limit. You chose how it's made when you [linked the node](/install/first-node/#the-server-data-volume): an image file, a disk of its own, or soft limits.

The node's **Disks** tab (admins) lists every disk in the machine, what each is used for, and where Raptor keeps data, with free space and how many servers are on each.

## Add a disk

A machine with a second disk can use it for servers or backups. The Disks tab offers to set up **empty** disks only (no partitions, no filesystem, nothing on it), and asks what to use it for:

- **For servers:** new servers can go on it, and existing ones can [move to it](/servers/settings/#move-to-another-disk). Pick which disk new servers use by default.
- **For backups:** it becomes a folder [backup destination](/servers/backups/#where-backups-go).

Setting up a disk formats it, so it's signed with an owner's passkey. A disk with anything on it is never formatted from the app: wipe it over SSH first if you're sure.

## Grow a disk

Made a disk bigger at your provider? **Grow by …** appears on the Disks tab once the machine sees the extra space; servers keep running. A disk on a partition needs the partition grown first (`growpart`).

To grow an image-file data volume on the machine itself:

```bash
sudo raptor storage status            # how big it is, and how full
sudo raptor storage grow -size 200GiB
```

## When a disk fills

A server at its disk limit can't write more: the game may stop saving. Raise its limit in [Settings](/servers/settings/), or free space (empty its trash). Alerts can tell you before it happens: see [Alerts](/team/alerts/).
