---
title: Connect your first node
description: Turn a fresh Linux machine into a Raptor node with one command.
sidebar:
  order: 1
---

Connecting a node takes one command and about five minutes. You need a machine that meets [the requirements](/start/requirements/) and an account on [app.raptorpanel.net](https://app.raptorpanel.net).

## 1. Make a join token

In the app, open **Nodes** and choose **Connect node**, then **Make a join token**. A token works once, for an hour.

If your org doesn't have a plan yet, the app asks you to start one first. Your first node is free for its first month.

## 2. Trust your passkey

The app asks you to sign with your passkey. The node then trusts that passkey from the moment it links, for dangerous actions like deleting a server. Your password manager asks twice: once to pick the passkey, once to sign.

You can skip this and pair a passkey later, by running `sudo raptor keys reset` on the node.

## 3. Run the command on the machine

The app shows a command like this one. Copy it from the app, since it holds your token:

```bash
curl -fsSL https://get.raptorpanel.net | sudo bash -s -- -token rpt_join_…
```

Log in to the machine (over SSH, for a VPS) and paste it. It:

1. Downloads Raptor and checks it against the checksum published with the release.
2. Checks the machine: a supported system, a VM rather than a container, systemd, and cgroups v2.
3. Installs Docker and the packages Raptor uses.
4. Creates a `raptor` user, installs Raptor, and writes its config.
5. Sets up the **server data volume**, where your servers' files live (below).
6. Starts Raptor and links the node to your org.

The app shows each step as it happens. When it says the node is linked and connected, you're done: choose **Open it**.

If you signed with your passkey in step 2, the command prints a fingerprint as it links. Check that it matches the one the app showed.

:::tip[Already running Docker?]
If other containers are running, the installer asks before restarting Docker, since that stops them. Add `-yes` to the command to answer yes in advance.
:::

### The server data volume

Raptor enforces each server's disk limit, so one server filling up can't take the others down with it. The installer asks how:

- **image** (the default): a file on this disk, formatted so disk limits are enforced. Game saves write at about half the speed of the bare disk; everything else runs at full speed, and players won't notice. It suggests three quarters of the free space as the size. You can grow it later with `sudo raptor storage grow`.
- **disk:** a separate disk you've already formatted as XFS and mounted at `/var/lib/raptor/volumes` with project quotas (`prjquota` in `/etc/fstab`), for full speed.
- **soft:** no hard limits. Raptor checks each server's usage every five minutes instead, and tells you when one is over.

To answer without being asked, add `-storage image -size 100GiB` (or `-storage soft`) to the end of the command.

## Next

[Deploy your first server](/install/first-server/).

## If the install fails

The installer stops at the step that failed and says why, and the app shows the same error. Fix what it says and run the same command again: it carries on where it stopped. If the token has expired by then, make a new one.

The common errors and their fixes are in [Install problems](/help/install-problems/).
