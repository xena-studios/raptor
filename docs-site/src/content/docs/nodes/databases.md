---
title: Database instances
description: Run MariaDB, MySQL, or PostgreSQL on a node for its servers, kept apart between networks.
sidebar:
  order: 4
---

A **database instance** is a database server Raptor runs on your node: MariaDB, MySQL, or PostgreSQL, in a container of its own. Servers on its network get their own database and user on it from their [Databases tab](/servers/databases/). Instances are included in the node's price, and your data stays on your machine.

## Make an instance

On the node's **Databases** tab (admins and owners), click **New instance**:

1. Pick the **engine**. MariaDB is what most Minecraft plugins expect; pick MySQL or PostgreSQL if the software asks for it.
2. Give it a **name** and its **memory**: 1 GB is plenty for a few servers' plugins.
3. If the node has [networks](/nodes/networks/), pick the **network** it's on. Only servers on that network can have databases on it, or reach it at all.

Its first start downloads the engine and sets it up, which takes a minute or two.

## Keep communities apart

Two communities on one node should each have their own [network](/nodes/networks/), with an instance on each. Three things then keep their data apart:

- **Separate instances:** each community's databases are on its own instance.
- **Separate networks:** an instance is reachable only from its network. On a network you made, it also answers only the servers that have a database on it, so a server that shares the network without having a database there can't reach it either.
- **Separate users:** every database has its own user, which can reach that database and nothing else. The instance's administrator account can only be used from the node itself.

On the default network every server can reach the instance, but each still has only its own user.

## Advanced settings

Open **Advanced** when making or editing an instance:

| Setting | What it does |
|---|---|
| **Version** | Which version of the engine it runs. The recommended one is picked for you. |
| **Max connections** | How many connections it takes at once; 100 unless set. |
| **CPU limit** | A hard limit, in percent of one core. |
| **Disk limit** | How much of the node's disk its data can use. |
| **Time zone** | The node's unless set, like `Europe/Berlin`. |
| **Also on** | More networks it joins, for servers on them. |

Changing an instance restarts it, which takes a few seconds; servers using it reconnect.

## Start, stop, and delete

The **⋯** menu next to an instance starts, stops, or restarts it. An instance comes back with the node after a restart.

**Delete** removes the instance and every database on it, with everything in them. It asks for the instance's name and a passkey.
