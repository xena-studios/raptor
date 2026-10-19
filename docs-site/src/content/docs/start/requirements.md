---
title: What you need
description: The machine, the system, the network, and the account a Raptor node needs.
sidebar:
  order: 2
---

Raptor runs your game servers on machines you own or rent, called **nodes**. The app at [app.raptorpanel.net](https://app.raptorpanel.net) manages them. The servers themselves, their files, and their backups stay on your node.

## The machine

- **A virtual machine (VPS) or a physical computer**, not a container. Raptor refuses to install inside one, so container-based plans (often sold as "LXC" or "OpenVZ") won't work. Look for "KVM" in the plan's description.
- **Debian 12, Debian 13, or Ubuntu 24.04**, freshly installed. Other systems aren't supported yet.
- **x86 (amd64) or ARM (arm64).** Both work. Some games only run on x86; the app says which when you deploy a server.
- **Memory:** Raptor keeps a tenth of the machine's memory free for the system (at least 1 GB, at most 4 GB), and the rest is for your servers. A Minecraft server for a few friends wants 2 to 4 GB, so 4 GB is a sensible minimum and 8 GB is comfortable.
- **Disk:** Raptor keeps 10 GB free for the system and gives your servers the rest. 40 GB or more is a good start.

## The network

- **Outgoing:** the node connects out to `api.raptorpanel.net` over HTTPS (port 443). Raptor never needs to connect in to your node, so it works behind a router.
- **Incoming:** players connect straight to your node, on each server's port (Minecraft's is 25565). On a VPS this usually just works, or needs a rule in your provider's firewall. At home, you [forward the port on your router](/help/players-cant-connect/#at-home).
- **SFTP** uses port 2022. Raptor keeps it closed until someone makes an SFTP login for a server.

## Your account

- **A passkey.** Raptor asks for one to sign dangerous actions, like deleting a server, so even someone who took over your account couldn't do them. Your phone, your computer, or a password manager can hold it.
- **Root access** to the machine (`sudo`), once, to run the install command.

## What it costs

Each node linked to your org is **$12 a month**, online or not, prorated by the day. Your first node is free for its first month: you add a card to start, and nothing is charged until the month ends. Every node includes unlimited servers, members, and schedules, and every feature. There are no limits on memory, CPU, or players: it's your hardware.
