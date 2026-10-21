---
title: System and firewall
description: The machine's hostname, time zone and clock, its firewall, and rebooting it, from the node's System tab.
sidebar:
  order: 5
---

The node's **System** tab (admins and owners) sets the machine itself, so you don't have to SSH in for the everyday things.

## Machine

- **Hostname:** follows the node's name by default. Turn that off to give the machine a hostname of its own.
- **Time zone:** servers get it as `TZ` the next time they start; schedules keep their own. **Use yours** sets it to your browser's.
- **Keep the clock in sync:** network time. Leave it on: a clock that drifts by minutes breaks backups to cloud storage and the node's connection.

## Firewall

Raptor manages a firewall for the machine itself, in its own nftables table, so Docker can't undo it.

It's listed like `ufw status`: each open port, what it's open to, from where, and why.

- **Opened by Raptor:** SSH; Raptor's SFTP port, only while someone has an SFTP password for the node; each server's ports (TCP and UDP), for its players; and a database instance's port when it has [external access](/nodes/databases/#reach-an-instance-from-outside), from just the addresses it allows. These are locked here: change them where they come from (the server's Network tab, the node's Settings or Databases tab). SSH is never blocked, so you can't lock yourself out.
- **Your rules:** allow or block ports (TCP, UDP, or both) from anywhere or from an address or range, each with a note. Allow rules come first, so they can make exceptions to block rules. Removing one asks first.
- **Block everything not listed:** with it on, connections to the machine that nothing above allows are dropped. With it off, anything listening on the machine can be reached.

Which servers can reach each other is set on the [Networks](/nodes/networks/) tab, not here.

**Other rules on the machine**, under the list, shows every rule already there from ufw, fail2ban, Docker, your provider's image, or anything else, so you can see what's really filtering traffic. It opens by itself when one of them drops traffic. Rules that only route traffic are folded away. Raptor never changes rules it didn't make.

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
