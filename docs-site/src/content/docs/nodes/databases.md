---
title: Database instances
description: Run MariaDB, MySQL, PostgreSQL, Redis, Valkey, or MongoDB on a node for its servers, kept apart between networks.
sidebar:
  order: 4
---

A **database instance** is a database server Raptor runs on your node: MariaDB, MySQL, PostgreSQL, Redis, Valkey, or MongoDB, in a container of its own. Its **databases** belong to it, each with its own user, and the servers you link to one get its details. Instances are included in the node's price, and your data stays on your machine.

**Databases** in the sidebar (admins and owners) lists every instance on every node, with its databases and the servers linked to each. A node's own **Databases** tab shows the same for that node.

## Make an instance

On the **Databases** page, click **New instance** on the node it should run on:

1. Pick the **engine**. MariaDB is what most Minecraft plugins expect; pick MySQL or PostgreSQL if the software asks for it. Redis is for caches and plugins that sync servers through it; Valkey is Redis' open-source fork, which the same plugins use unchanged. MongoDB is for bots and apps that store documents.
2. Give it a **name** and its **memory**: 1 GB is plenty for a few servers' plugins.
3. If the node has [networks](/nodes/networks/), pick the **network** it's on. Every server on that network can reach it; servers on other networks can't.

Its first start downloads the engine and sets it up, which takes a minute or two.

## Databases and linked servers

- **New database** under an instance makes a database and its own user, with a random password, shown once. **Password** shows it again (with your passkey), and the key button gives it a new one.
- **Link server** gives a server the database's details as variables at its next start (`DB_HOST`, `DB_PASSWORD`, `DATABASE_URL`, and the rest: see [the server's Databases tab](/servers/databases/)). Link as many servers as share it, like a network of Minecraft servers sharing LuckPerms. A server has to be on the instance's network.
- **Unlink** (the × beside a server) stops the server getting the details. The database and its data stay.
- **Deleting a server never deletes a database:** its links go, and the database stays here.
- **Delete** removes the database and everything in it, with its users, even if servers are linked. It asks for its name and a passkey.

A database made from a server's own Databases tab is linked to that server, and shows here too.

## Redis, Valkey, and MongoDB

- **Valkey works exactly like Redis** here: pick it if you'd rather run the open-source fork (or your plugin's docs suggest it). Everything below about Redis is true of Valkey too.
- **A Redis instance holds one database.** Redis can't keep two databases apart, so make one Redis instance for each thing that needs it; 256 MB is plenty for most. Several servers can be linked to that one database, like servers syncing through it. Its user can do everything apps do, but not change Redis' settings or load modules.
- **MongoDB instances hold many databases** like MariaDB's: each database has its own user, which reaches that database and nothing else. MongoDB 8.0 and later need a processor from about 2016 or later; pick 7.0 on older machines.
- MongoDB changes version one major release at a time (7.0 to 8.0, then 9.0).
- On machines with Linux 6.19 or newer (Ubuntu 26.04, some cloud kernels), MongoDB 8.0 and 9.0 don't start, a problem MongoDB is fixing; Raptor offers 7.0 there and says why.
- Redis and Valkey can be [reached from outside](#reach-an-instance-from-outside), on a port that only takes TLS. MongoDB can't: it would need every plugin inside to use TLS too.

## See what an instance uses

Under each running instance, the Databases page shows what it uses now: its **memory** and **disk**, each of its limit, its **connections** of its max connections, **queries** a second, and how long it's been up (its **uptime**). They're measured every 15 seconds. A bar turns amber past 75% and red past 90%.

What counts as a query depends on the engine: statements for MariaDB and MySQL, transactions for PostgreSQL, commands for Redis and Valkey, and operations for MongoDB. Raptor's own checks aren't counted, so an instance nothing uses reads 0. Queries a second shows once two measurements are in, so give a just-started instance 15 seconds.

An instance's own page (**Open**) shows the same at the top, with the last 10 minutes of its connections and queries. Nodes need an up-to-date Wings for these.

## Look inside an instance

**Open** next to a running MariaDB, MySQL, or PostgreSQL instance shows its own page (admins and owners):

- **Databases:** every database on it. Each opens in the same browser as the server's [Databases tab](/servers/databases/#browse-and-change-a-database), with its rows, structure, SQL, imports, and exports, through a server linked to it; link one to browse a database that has none.
- **Users:** everyone who can log in to the instance and the databases each can use: each database's own user, its read-only user if it has one, and the instance's administrator, which only Raptor uses. **Make one** gives any database a read-only user for a tool outside Raptor.
- **Connections:** every connection to the instance, from every server, with what it's running, and stopping a slow query.

A database opened here is changed as its own user, like from its server's tab, so nothing on this page reaches further than the server itself could.

## Keep communities apart

Two communities on one node should each have their own [network](/nodes/networks/), with an instance on each. Three things then keep their data apart:

- **Separate instances:** each community's databases are on its own instance.
- **Separate networks:** an instance is reachable only from its network. Every server on that network can connect to it, and no server on another network can.
- **Separate users:** every database has its own user, which can reach that database and nothing else. A server can connect to the instance, but only gets into a database with its user and password, which only linked servers are given. The instance's administrator account can only be used from the node itself.

On the default network every server can reach the instance, but each still needs a database's own user.

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
| **Engine settings** | Any other setting of the engine's, one `name = value` per line. See [Engine settings](#engine-settings). |

Changing an instance restarts it, which takes a few seconds; servers using it reconnect.

### Engine settings

For a setting that isn't a field, write it under **Engine settings**, one per line, the way the engine's own documentation names it:

```
max_allowed_packet = 64M
slow_query_log = ON
long_query_time = 1
```

Raptor takes only settings that are safe to change: none that name a file or folder, load plugins or modules, change how users log in, or change where the engine listens. Each is checked as you type, and again by the node when you save; a line that isn't right says why, with its line number.

- **MariaDB and MySQL:** InnoDB's buffer pool and log settings, `max_allowed_packet`, timeouts, caches and buffers, the slow query log (`slow_query_log`, `long_query_time`), `sql_mode`, the character set and collation, transaction isolation, and the event scheduler. Sizes are like `64M` or `1G`.
- **PostgreSQL:** `shared_buffers`, `work_mem`, `maintenance_work_mem`, `effective_cache_size`, the planner's costs, WAL and checkpoints, `synchronous_commit`, `statement_timeout` and the other timeouts, `log_min_duration_statement` for slow queries, autovacuum, parallel workers, and JIT. Sizes are like `64MB`, times like `30s`.
- **Redis and Valkey:** `maxmemory-policy` (to drop keys at the memory limit instead of refusing writes, like a cache), `notify-keyspace-events`, `timeout`, `hz`, the slow log, lazy freeing, `appendfsync`, and the small-encoding limits. Sizes are like `64mb`.
- **MongoDB** has no engine settings.

Memory settings like the buffer pool can't be more than the instance's memory. Max connections and the time zone have fields of their own above.

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

1. Pick a **port** for it on the node (13306, 15432, or 16379 is suggested).
2. List **who may connect**: your home or office's public address, or your app's server. Ranges like `198.51.100.0/24` work too. Nothing else can reach the port, not even other things on the node.
3. **Turn on with passkey.** The instance restarts, which takes a few seconds.

Connect to the node's address on that port, as one of the server's databases' users (from its [Databases tab](/servers/databases/)). Connections from outside must use **TLS**: download the instance's **Certificate** from the same dialog and give it to your tool so it can check it's really your instance (in DBeaver, the SSL tab's CA certificate; for `psql`, `sslmode=verify-full sslrootcert=<file>`; for `mysql`, `--ssl-ca=<file> --ssl-mode=VERIFY_IDENTITY`; for `redis-cli`, `--tls --cacert <file>`, or a `rediss://` URL in apps).

Servers on the instance's network keep connecting as before, without TLS, so plugins don't notice anything. On Redis and Valkey, the port you pick only takes TLS, and the one servers use stays inside the network.

**Turn off** in the same dialog closes the port again.

## When something's wrong

Raptor watches each instance. One that stops or keeps restarting is started again by itself; one that can't start says why in the list (often **out of memory**: give it more memory).

To hear about it, add a rule on **Settings → Alerts** for an instance **down or restarting**, its **disk** or **connections** near their limits, or a **version change** finishing or failing ([Alerts](/team/alerts/)).

An instance whose connections sit near its max connections turns plugins away when it fills; raise **Max connections** under **Advanced**, or look at which server holds them on its own page's **Connections**.

On the node itself, `raptor db ps` lists the instances with their memory, disk, and connections, and `sudo raptor doctor` checks each one is running, answers, and is on its networks ([raptor doctor](/cli/doctor/)).

## Start, stop, and delete

The **⋯** menu next to an instance starts, stops, or restarts it. An instance comes back with the node after a restart.

**Delete** removes the instance and every database on it, with everything in them, whichever servers are linked. It asks for the instance's name and a passkey.
