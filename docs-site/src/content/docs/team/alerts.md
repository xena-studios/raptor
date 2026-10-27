---
title: Alerts
description: Get told when a server crashes, a machine goes offline, or a disk fills up, in the app, by email, or on Discord, Slack, Telegram, ntfy, or a webhook.
sidebar:
  order: 2
---

Alerts tell the right people when something goes wrong, so you hear about a crashed server before your players do. Admins decide **what** alerts and **who** hears about it; each person decides **how** they're told.

## Your inbox

The **bell** at the top right of every page is your inbox: every alert sent to you, from every org you're in, for 30 days, each linking to the page it's about. The count on it is what's new since you last opened it.

## How you're told

Open the bell, then its settings, to choose where else your alerts go:

- **Email**, to your account's address (on from the start; turn it off if you like).
- **Telegram**, **ntfy** (push to your phone), **Discord**, **Slack**, or a **webhook**.

Each has a **Test** button. Only you see your channels. One that fails for 3 days is turned off, and you're emailed.

## What alerts, and who hears

On **Settings → Alerts** (admins and owners), each **rule** is:

1. **A condition** (below),
2. **where** it applies: every node, one node, or one server,
3. **who it tells**: people by role (owners, admins, members) or by name, and
4. **team channels** it also posts to, if any.

Members are only told about servers they have access to, and never about database instances: those alerts go to owners and admins, whoever else a rule names.

New orgs start with rules that tell their owners about crash loops, offline nodes, and low disk.

### Conditions

| Server | Node |
|---|---|
| Crashed | Offline (after 5 minutes, or the grace you set) |
| Down after repeated crashes | Disk running low |
| Install finished or failed | Wings updated or rolled back |
| Backup failed | Signed actions and passkey changes |
| Restore finished or failed | CPU, memory, or disk above a percent, for a while |
| Over its disk limit | |
| CPU, memory, or disk above a percent, for a while | |

**Database instances** (on [the node's Databases tab](/nodes/databases/)):

- Down or restarting: it stopped, or kept restarting. Raptor starts it again by itself, and tells you once it answers again.
- Disk above a percent of its disk limit, for a while. Instances without a disk limit aren't checked.
- Connections above a percent of its max connections, for a while.
- A version change, patch update, or going back finished or failed.

They need Wings v0.11.0 or later on the node.

Conditions that end (a node back online, a disk with room again, an instance back up) say so too, unless the rule says not to.

## Team channels

Places the whole team reads, added on **Settings → Alerts**: **Discord** and **Slack** (webhook URLs), **Telegram** (a group with Raptor's bot), **ntfy** (a topic), and **webhooks**.

Webhooks are JSON, signed: `X-Raptor-Signature: sha256=<hex>` is the HMAC-SHA256 of `<timestamp>.<body>` with your secret, and `X-Raptor-Timestamp` and `X-Raptor-Event` say when and what. Check the signature, and reject old timestamps.

Secrets (webhook URLs, tokens) are encrypted and never shown again. The last 50 deliveries, with any errors, are listed on the page.
