---
title: Networks
description: Keep groups of servers on one node from reaching each other, and put a proxy on more than one network.
sidebar:
  order: 3
---

Every server on a node starts on the **default network**, where servers can reach each other. A proxy like Velocity relies on that to reach its backends. When two communities share one machine, give each its own **network**. Servers on different networks can't reach each other, even on ports bound to `127.0.0.1`.

Players reach every server's ports whatever network it's on.

The node's **Networks** tab (admins and owners) lists its networks and the servers on each. It also lists the machine's other Docker networks, read-only. Raptor never changes networks it didn't make.

## Make a network

1. On the node's **Networks** tab, click **New network** and give it a name, like "Community A".
2. Open each server that belongs to it, go to its **Network** tab, and pick the network under **Networks**.
3. Restart the server. It moves to the new network at its next start.

When you deploy a server on a node that has networks, you pick its network then.

Servers find each other on a network by name: "Survival SMP" is `survival-smp.raptor`. Each server also gets a name from its ID, like `0199a3b2.raptor`, which stays the same if it's renamed. A server's Network tab shows both.

## A proxy for several networks

A server can be on more than one network: its own, plus others under **Also on**. A Velocity or BungeeCord proxy on Community A's and Community B's networks reaches the backends on both, while the backends still can't reach each other.

## Advanced settings

Open **Advanced** when making or editing a network:

| Setting | What it does |
|---|---|
| **Internet access** | Off keeps its servers from connecting out, for backends that should only talk to their proxy. Players still reach their ports. |
| **Reach the machine** | Off keeps its servers from connecting to services on the node itself. |
| **IPv6** | Gives the network an IPv6 range too. |
| **Subnet** | Its address range. Raptor picks a free one unless you set a private range, like `10.210.0.0/24`. |
| **MTU** | Lower it if the machine is behind a VPN or a provider that needs it. |

Changing the subnet, internet access, IPv6, or MTU recreates the network, so stop its servers first. Renaming it and changing **Reach the machine** work at any time.

## Delete a network

Move its servers to another network first, then click the bin next to it. A network with servers on it can't be deleted.
