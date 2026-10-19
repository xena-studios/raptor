---
title: raptor doctor
description: Check a node from top to bottom, see how to fix what's wrong, and send support a diagnostics file.
sidebar:
  order: 2
---

`sudo raptor doctor` checks the machine and Raptor on it, and for each problem says **what's wrong, why it matters, and how to fix it**. It never changes anything, and it works even when Wings is down.

```bash
sudo raptor doctor
```

Each check passes, **warns** (it works, but should change), **fails** (broken, or will break servers), or is skipped (doesn't apply). It exits with 1 if anything failed, so scripts can tell; `-json` prints the results for them.

The same checks run on the node's **Health** tab in the app, and Raptor runs them every hour by itself.

## What it checks

- **The system:** a supported OS (Debian 12 or 13, Ubuntu 24.04; others warn), the kernel, cgroups v2, systemd, and Raptor's services running.
- **Docker:** reachable, version 24 or newer, with `live-restore` on (without it, a Docker restart stops every server).
- **Storage:** the server data volume mounted with disk limits, each extra disk, and free space everywhere Raptor and Docker keep things.
- **The clock** in sync. A clock minutes off breaks the node's connection, schedules, and backups to cloud storage.
- **Raptor's firewall** in place.
- **The Panel:** reachable, the node's key present, and connected. If it isn't, it says why and how to fix it (a removed node needs `raptor relink`).
- **The node's address** resolving to the IP the Panel sees.
- **SFTP** answering, when it's on.
- **Pterodactyl** on the same machine not in Raptor's way.
- **Security warnings** (never changed for you): password logins for root over SSH, and automatic security updates off.

## Getting help with a support code

```bash
sudo raptor doctor -upload
```

This writes a diagnostics file and sends it to Raptor's support over HTTPS, then prints a **support code** like `RPT-7K2M-QX9D`. Give support that code in your ticket ([Get help](/help/get-help/)).

What's in it: doctor's results, Raptor's and Docker's logs, Docker's and the system's state, the config file, and the install log. **No server files and no secrets:** keys, tokens, passwords, and anything named like one are replaced with `[redacted]`.

It works even if the node can't connect to the Panel, which is often when you need it. To look at the file yourself, or send it another way, use `-bundle` instead: it's written to `/var/tmp`.
