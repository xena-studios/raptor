---
title: Database instances
description: Run MariaDB, MySQL, or PostgreSQL on a node for its servers, kept apart between networks.
sidebar:
  order: 4
---

A **database instance** is a database server Raptor runs on your node: MariaDB, MySQL, PostgreSQL, Redis, or MongoDB, in a container of its own. Servers on its network get their own database and user on it from their [Databases tab](/servers/databases/). Instances are included in the node's price, and your data stays on your machine.

## Make an instance

On the node's **Databases** tab (admins and owners), click **New instance**:

1. Pick the **engine**. MariaDB is what most Minecraft plugins expect; pick MySQL or PostgreSQL if the software asks for it. Redis is for caches and plugins that sync servers through it, MongoDB for bots and apps that store documents.
2. Give it a **name** and its **memory**: 1 GB is plenty for a few servers' plugins.
3. If the node has [networks](/nodes/networks/), pick the **network** it's on. Only servers on that network can have databases on it, or reach it at all.

Its first start downloads the engine and sets it up, which takes a minute or two.

## Redis and MongoDB

- **A Redis instance holds one server's data.** Redis can't keep two servers' data apart, so make one Redis instance per server that needs it; 256 MB is plenty for most. Its server's user can do everything apps do, but not change Redis' settings or load modules.
- **MongoDB instances are shared** like MariaDB's: each server's database has its own user, which reaches that database and nothing else. MongoDB 8.0 and later need a processor from about 2016 or later; pick 7.0 on older machines.
- MongoDB changes version one major release at a time (7.0 to 8.0, then 9.0).
- External access isn't available for Redis and MongoDB yet.

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
| **Patch updates** | When it restarts for a newer patch of its version: 04:00 in the node's time unless set, or only when you say. |

Changing an instance restarts it, which takes a few seconds; servers using it reconnect.

## Change its version

**Change version** in the **⋯** menu moves an instance to a newer version of its engine, like MariaDB 11.4 to 11.8 or PostgreSQL 16 to 17. It has to be running. Raptor then:

1. counts the rows in every table of every database;
2. keeps a copy of its data, stops it, and starts the new version (for a new PostgreSQL major, it moves every database over to the new version, users and all);
3. counts the rows again.

If the new version doesn't start, or anything differs, it's put back as it was by itself, and the instance says what went wrong. Servers using it lose their connection while it switches, usually for under a minute.

For **7 days** after a change, **Go back** in the menu puts the old version and its data from just before the change back. Anything written to its databases since is lost, so it asks for your passkey. After that, an instance can't go down a version: older versions can't read a newer one's data. The copy kept for going back counts toward the instance's disk limit, if it has one.

### Patch updates

Wings updates can bring newer patches of a version (11.8.3 to 11.8.4). The instance then shows **Update available** and gets it at the start of its maintenance window (04:00 in the node's time, or the hour you set under **Advanced**), the same careful way. **Update to the newest patch** in the menu does it now. Patches wait while an instance can still go back from a version change.

## Reach an instance from outside

To connect a desktop tool like DBeaver, or an app running somewhere else, open **External access** in the instance's **⋯** menu:

1. Pick a **port** for it on the node (13306 or 15432 is suggested).
2. List **who may connect**: your home or office's public address, or your app's server. Ranges like `198.51.100.0/24` work too. Nothing else can reach the port, not even other things on the node.
3. **Turn on with passkey.** The instance restarts, which takes a few seconds.

Connect to the node's address on that port, as one of the server's databases' users (from its [Databases tab](/servers/databases/)). Connections from outside must use **TLS**: download the instance's **Certificate** from the same dialog and give it to your tool so it can check it's really your instance (in DBeaver, the SSL tab's CA certificate; for `psql`, `sslmode=verify-full sslrootcert=<file>`; for `mysql`, `--ssl-ca=<file> --ssl-mode=VERIFY_IDENTITY`).

Servers on the instance's network keep connecting as before, without TLS, so plugins don't notice anything.

**Turn off** in the same dialog closes the port again.

## Start, stop, and delete

The **⋯** menu next to an instance starts, stops, or restarts it. An instance comes back with the node after a restart.

**Delete** removes the instance and every database on it, with everything in them. It asks for the instance's name and a passkey.
