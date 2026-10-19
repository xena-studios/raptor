---
title: How Raptor protects you
description: What Raptor can and can't do on your machine, why dangerous actions need your passkey, and where your data lives.
sidebar:
  order: 1
---

A panel that manages your servers has a lot of power over your machine: Wings runs as root. Raptor is built so that the parts that could hurt you are the parts that need **you**, not just a Raptor account.

## Dangerous actions need your passkey

These are **signed by your passkey** in your browser, and **checked by Wings on your machine** before they run:

- deleting a server, or wiping and reinstalling it;
- restoring a backup over a server;
- changing what a server runs: its game, runtime, or startup command;
- saving the firewall, rebooting, formatting a disk;
- trusting another passkey, or letting Raptor's support in.

Your passkey signs the exact request (this server, this action, these settings), with a short expiry. Wings checks the signature against the passkeys it trusts, which are set from your machine when you link it, and refuses anything else. **So even someone who took over Raptor's own servers couldn't do these without you.**

Everyday actions (power, console commands, files, backups) need only a session in the app and the right [permissions](/team/members/#what-members-can-do-on-a-server).

## Wings only does what it's built to do

- Wings accepts **specific, typed commands**, like "restart this server" or "write this file". It never runs a shell command sent to it.
- Your machine connects **out** to Raptor, and opens no port for it. Both sides prove who they are with keys; the node's key is made on the machine and never leaves it.
- Every command from the Panel carries a short-lived grant for exactly that action, which Wings checks itself.
- Wings releases are **signed** with Raptor's offline key, and each node checks the signature before it installs an update.

## Servers are kept apart

- Each server runs in its own hardened container, as an unprivileged user, with its memory, CPU, and disk limits enforced by the kernel.
- File access goes through code that can't be led outside the server's folder by a link, whatever the game does.
- No server can reach your cloud provider's metadata service, where credentials live. Install scripts can't reach your private network or the machine itself.

## Your data

- **Server files stay on your machine.** Players connect straight to it. The app's file manager passes files through Raptor's network, encrypted; for files you'd rather never did, use SFTP, which goes straight to your node.
- **Backups are encrypted on your machine** before they're uploaded anywhere. By default Raptor keeps a sealed copy of the key so you can recover a dead machine; [keep it yourself](/servers/backups/#the-backup-key) if you'd rather Raptor couldn't.
- **Backup destinations' credentials** live on your node, never in the Panel.
- **Your account** has no password to leak. See [Your account and security](/team/account-security/).

## Support needs your permission

Raptor's staff can only work on a node with your consent, signed with your passkey, for a time and level you choose. They get typed commands only (never a shell), can't do anything that needs your passkey, and everything they do is recorded on the node. See [Support access](/nodes/overview/#support-access).

## What the limits are

Said plainly:

- Someone who gets into your Raptor account could do the everyday things you can: stop servers, change files. Use a passkey or two-factor, so an inbox alone isn't enough.
- Raptor's website runs behind Cloudflare, which can see traffic through the app (consoles, the file manager). SFTP doesn't go through it.
- Root on your machine is root: anyone with it can do anything there, Raptor or not.

## Open source

Every line that runs on your machine is public, under the AGPL: [github.com/xena-studios/raptor](https://github.com/xena-studios/raptor). Found a problem? [Report it privately](/help/get-help/#report-a-security-issue).
