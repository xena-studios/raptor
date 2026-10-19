---
title: Nodes
description: A node's overview and health, its address, removing a node, and support access.
sidebar:
  order: 1
---

A **node** is a machine you've linked to your org. Its page has tabs for its **Overview**, **Health**, **Backups**, **Disks**, **Databases**, **Networks**, **System**, **Activity**, and **Settings** (all but Overview for admins and owners).

## Overview

- **What it's using:** CPU, memory, server storage, and network, with charts over the last hour, day, or week.
- **Its servers,** with what each is using now.
- **Details:** its address, public IPs (hidden until revealed), architecture, cores, memory, Wings version, uptime, and when it was linked.

## Health

The **Health** tab is the machine up close: load, memory and swap, every disk Raptor uses and how full it is, network interfaces, and what each server uses. Its checks are [`raptor doctor`](/cli/doctor/)'s, run on the machine: each says what's wrong, why it matters, and how to fix it. **Check again** runs them now. Raptor runs the checks every hour too, and the overview shows when something needs attention.

## Its address

Every node gets an address like `1cdd002cd9a3.raptornodes.net` that follows its IP, even on a home connection whose IP changes. Players can connect to it with the server's port, and SFTP uses it.

Since it leads to your machine's IP, the app hides it (and the IPs) until you reveal it, so it isn't on screen while you stream; you can copy it without revealing it. Members never see the node's IPs. For players, you can give a server its own **shown address** (a domain, or a proxy) on its Network tab.

## Renaming

Rename a node on its **Settings** tab. The machine's own hostname follows the name ("Basement Box #2" becomes `basement-box-2`) unless you give it one of its own on the **System** tab. The `raptornodes.net` address doesn't change.

## Removing a node

**Remove node** on its **Settings** tab (admins and owners) unlinks it from your org:

- Its servers **keep running** on the machine; you just can't manage them from Raptor.
- It stops counting toward your plan.
- Its address, Raptor Backup Storage (kept 30 days), and members' access to its servers go.

To bring it back, make a join token and run `sudo raptor relink -token rpt_join_…` on the machine. To take Raptor off the machine entirely, see [Leaving Raptor](/help/faq/#how-do-i-leave-raptor).

## More than one node

Link as many machines as you like: each is $12 a month. In the app, choose **Connect node** again and run its command on the new machine. Servers are made on one node and stay there; to move one, back it up and restore it, or copy its files with [Import](/servers/files/#import-from-another-host).

## Support access

If you ask Raptor's support for help, they may request **access to a node**: diagnostics only, operating its servers, or managing their files too, for a set time, with a reason and a ticket. Owners and admins are emailed, and the node's pages show the request.

- **Approve** it with an owner's passkey (you can give less than was asked, never more), or **Deny** it.
- While it lasts, the node's pages show who has what until when, with **End now**.
- Everything staff do is in the node's activity, marked as staff.
- Staff never get a shell, and can't do anything that needs your passkey.

Turn support access off for a node entirely on its **Settings** tab, or with `sudo raptor support off` on the machine.
