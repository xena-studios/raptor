---
title: Install problems
description: What the installer's errors mean, and how to fix each one.
sidebar:
  order: 3
---

When the install command fails, it stops at the failed step and prints why, like this:

```text
raptor: checking the box: this is a container; Raptor runs on a VM or a physical box
```

The app shows the same error next to the step. Fix what it says, then run **the same command** again: it skips what's already done and carries on. If an hour has passed, the token has expired, so make a new one in the app.

For anything not listed here, run `sudo raptor doctor` on the machine. It checks the node and says, for each problem, what's wrong and how to fix it.

## Checking the box

**`this is a container; Raptor runs on a VM or a physical box`**
Your plan runs the machine in a container (often sold as LXC or OpenVZ), where Raptor can't enforce resource limits. Switch to a KVM or other virtual machine plan, or use a physical computer.

**`… isn't supported yet (Debian 12 and 13, Ubuntu 24.04 are); -force continues anyway`**
Reinstall the machine with one of those systems; most providers offer them. `-force` carries on regardless, but on an unsupported system you're on your own if something breaks.

**`systemd isn't running; Raptor needs it`** or **`cgroups v2 isn't enabled`**
This usually means a container too, or a very old system. A fresh install of a supported system has both.

**`architecture … isn't supported (amd64 and arm64 are)`**
Raptor runs on 64-bit x86 and ARM machines only.

## Docker

**`Docker … is too old (24 or newer)`**
Docker was already installed, from an older package. Update it from [Docker's own repository](https://docs.docker.com/engine/install/), or remove it and run the command again so the installer adds the current one.

**`… container(s) are running; restarting Docker stops them. Restart now?`**
Not an error: the installer needs to restart Docker to apply its settings, and asks first because that stops containers you already run. Answer `y`, or add `-yes` to the command to agree in advance.

## Server data volume

**`/var/lib/raptor/volumes isn't mounted: mount your XFS data disk there …`**
You picked **disk**, which uses a separate disk you've prepared. Format it as XFS, mount it at `/var/lib/raptor/volumes` with `prjquota` in `/etc/fstab`, then run the command again. Or run it again and pick **image**, which needs no extra disk.

**`/var/lib/raptor/volumes is mounted (…) without XFS project quotas`**
Something is already mounted there, but disk limits can't work on it. Add `prjquota` to its options in `/etc/fstab` (it must be XFS), or unmount it.

## Linking to the Panel

**`the join token is invalid, expired, or already used: make a new one in the Panel`**
A token works once, for an hour. Make a new one under **Nodes → Connect node**, and run the new command it shows.

**`this node is already linked (as …); to link it again, run raptor relink`**
The machine is already a node. If you removed it from the app and want it back, make a new token and run `sudo raptor relink -token rpt_join_…` instead.

**`linked, but Wings hasn't connected yet`**
The node is linked but can't reach Raptor. Check the machine can connect out to `api.raptorpanel.net` on port 443, then look at `sudo raptor status` and `journalctl -u raptor-wings`.

**`the owner passkey the Panel sent wasn't trusted`**
The node linked, but without your passkey. Pair it by running `sudo raptor keys reset` on the node.
