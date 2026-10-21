---
title: Get help
description: Where to ask questions, report bugs, request games, and reach the Raptor team.
sidebar:
  order: 1
---

In the app, your account menu (bottom left) links here and to Discord.

## Ask the community

Raptor's [Discord server](https://discord.gg/HyUa3sRpeN) is where questions, bug reports, and requests go:

- **🤝 community-help:** how do I…? Questions about setting up a node, a game, or a provider.
- **🐛 bug-reports:** something in Raptor is broken. Say what you did, what you expected, and what happened instead.
- **💡 feature-requests:** an idea for Raptor.
- **🥚 egg-requests:** a game you'd like in the catalog.
- **📢 announcements** and **🚀 releases:** what's new, and when.
- **🟢 status:** incidents and planned downtime for the app.

## Reach the Raptor team

For anything about your account, your bill, or a specific node, use **🎫 open-a-ticket** on the Discord server: it's private between you and the team. Don't post join tokens, SFTP passwords, or your node's address in public channels.

### Send a diagnostics bundle

When a node misbehaves, the team will usually ask for its diagnostics. Run this on the node:

```bash
sudo raptor doctor -upload
```

It checks the node, writes a bundle with keys and anything that looks like a password or token removed, and uploads it to Raptor support. It prints a **support code** like `RPT-7K2M-QX9D`: give that to the team. To keep the bundle on the machine instead, use `-bundle`; it's written to `/var/tmp`.

### Let the team look at your node

If the team needs to see your node to help, they ask for **support access**, and the request shows on the node's pages in the app. You approve it with your passkey, for as long as you choose (at most 7 days), and at the level you choose:

- **Diagnostics:** the node's health, checks, metrics, and Raptor's log.
- **Operate:** that, and starting, stopping, and restarting servers and using their consoles.
- **Manage:** that, and reading and changing servers' files, schedules, and backups.

The team can never do what needs your passkey, like deleting a server. You can end their access at any time, from the app or with `sudo raptor support revoke all` on the node. Everything they do is logged, reads included. To turn support access off on a node for good, run `sudo raptor support off`.

## Report a security issue

Don't post security problems on Discord. Email **security@raptorpanel.net**, or use [GitHub's private vulnerability reporting](https://github.com/xena-studios/raptor/security/advisories/new). The [security policy](https://github.com/xena-studios/raptor/blob/main/SECURITY.md) says what happens next.
