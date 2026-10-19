---
title: System and firewall
description: The machine's hostname, time zone and clock, its firewall, and rebooting it, from the node's System tab.
sidebar:
  order: 3
---

The node's **System** tab (admins and owners) sets the machine itself, so you don't have to SSH in for the everyday things.

## Machine

- **Hostname:** follows the node's name by default. Turn that off to give the machine a hostname of its own.
- **Time zone:** servers get it as `TZ` the next time they start; schedules keep their own. **Use yours** sets it to your browser's.
- **Keep the clock in sync:** network time. Leave it on: a clock that drifts by minutes breaks backups to cloud storage and the node's connection.

## Firewall

Raptor manages a firewall for the machine itself, in its own nftables table, so Docker can't undo it.

- **Block incoming connections:** new connections to the machine are dropped unless a rule allows them. SSH, Raptor's SFTP, and servers on the host's network stay open, listed as **Always open**, so you can't lock yourself out.
- **Rules:** allow or block ports (TCP, UDP, or both) from anywhere or from an address or range, each with a note. Allow rules come first, so they can make exceptions to block rules.
- Game servers' ports are opened by the servers themselves, and stay open unless a rule blocks them.

The table also lists **every rule already on the machine**, from ufw, fail2ban, your provider's image, or anything else, in the same columns, so you can see what's really filtering traffic. Rules that only route traffic for Docker and Raptor are folded away. Raptor never changes rules it didn't make.

Saving the firewall is signed with your passkey.

:::caution[ufw]
If ufw is on, it filters connections to the machine before Raptor's rules, so an address ufw blocks stays blocked whatever you allow here. Game servers aren't affected.
:::

## Power

**Reboot with passkey** restarts the machine, after a kernel update or when it misbehaves. Servers stop properly first, and those that were running start again once it's back, usually within a few minutes. The node shows offline for a moment.

## What Raptor does to the machine

Raptor keeps its own firewall table that also:

- keeps every server from reaching your cloud provider's metadata service, where credentials live;
- keeps install scripts off your home or private network and the machine itself (game servers can still talk to each other, for proxies like Velocity).

It also warns, without changing anything, about risky setups it notices: password logins for root over SSH, security updates turned off, a low disk, a drifting clock.
