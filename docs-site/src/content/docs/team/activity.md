---
title: Activity
description: Who did what, when, and from where, for your account, your org, a node, or a server, and how to search it.
sidebar:
  order: 3
---

Raptor records everything done to your servers and your team, with who did it, when, and from which device and IP address, and keeps it for a year.

## Where to find it

- **Your account:** **Settings → Activity** (the menu under your name): sign-ins, failed attempts, and changes to how you sign in.
- **Your org:** **Settings → Activity** in the org (admins and owners): members, invitations, nodes, access, and everything done to servers.
- **A node:** its **Activity** tab: what was done to it and the servers on it.
- **A server:** its **Activity** tab.

## Searching it

Every activity list can be narrowed:

- **Search:** words in emails, names, devices, and details.
- **IP or range:** one address, or a range like `203.0.113.0/24`.
- **When:** today, the last 7 or 30 days, or two dates.
- **Kind of event:** power, console, files and SFTP, backups, schedules, server changes, access, members, nodes, eggs, support, and more.
- **Who:** one member.
- **Where:** in the org's log, one node or server.
- **Failed only:** failed sign-ins, and commands that failed.

Click an IP address or a person in the list to narrow it to them.

## On the machine

Each node keeps its own record of signed actions (deleting servers, restores, trusted keys) and key resets, which Raptor can't change:

```bash
sudo raptor audit -since 72h
```
