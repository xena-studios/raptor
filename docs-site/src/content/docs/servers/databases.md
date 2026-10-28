---
title: Databases
description: Give a server its own MariaDB, MySQL, or PostgreSQL database, and connect plugins to it.
sidebar:
  order: 7
---

Plugins like LuckPerms and CoreProtect, and most bots and web apps, want a database. A server's **Databases** tab gives it one on a database instance running on the same node, included in the node's price. Each database has its own user, which reaches that database and nothing else.

## Make a database

1. An admin makes a **database instance** on the node first (see [Database instances](/nodes/databases/)). One instance serves every server on its network.
2. On the server's **Databases** tab, click **New database** and give it a name, like `luckperms`.
3. Copy the password from the dialog that opens. You can show it again later with **Password**, which asks for your passkey.

## Connect a plugin

Each database shows its **Host**, **Database**, and **User**, with the password behind **Password**. Put those in the plugin's config. For LuckPerms, in `plugins/LuckPerms/config.yml`:

```yaml
storage-method: MariaDB
data:
  address: community-a-mariadb.db.raptor:3306
  database: s3e4f5a6b7c8d_luckperms
  username: s3e4f5a6b7c8d_luckperms
  password: the password
```

The host is a name, like `community-a-mariadb.db.raptor`, that works from the node's servers. It stays the same even if the instance moves to another address.

### MySQL 8 and newer

Plugins on the node connect without TLS, and MySQL 8 then makes the client fetch the server's key before it can sign in. Plugins that use MySQL's Java driver fail with **"Public Key Retrieval is not allowed"** unless you allow it. In LuckPerms, add it under `pool-settings` → `properties`:

```yaml
    properties:
      useUnicode: true
      characterEncoding: utf8
      allowPublicKeyRetrieval: true
```

Other plugins take the same setting as `allowPublicKeyRetrieval=true` at the end of their JDBC URL. For the `mysql` client, add `--get-server-public-key`. MariaDB and PostgreSQL don't need this.

## From outside

If an admin gave the database's instance [external access](/nodes/databases/#reach-an-instance-from-outside), its row also shows its address **from outside**, and a link to its certificate. Only the addresses the admin listed can connect there, with TLS.

## Redis and MongoDB

A database on a **Redis** instance has a user and a password, but no name: Redis' databases are numbered, and yours is 0. Each Redis instance is for one server, so pick one nothing else uses. A **MongoDB** database's user is stored in the database itself, so connect with `authSource` set to its name; the URL Raptor gives (`MONGODB_URI`) has it.

**Open** on a Redis database shows its keys: search them with a pattern like `player:*`, see each one's value and when it expires, and with **Change databases** edit them, add new ones, or run Redis commands from the **Command** tab. On a MongoDB database it shows its collections and their documents: find documents with a filter like `{"name": "alex"}`, sort them, and edit, insert, or delete them as JSON. MongoDB databases can be exported and imported as `mongodump` archives; a Redis database's data is in the server's backups, and **Restore from a backup** puts it back.

## Variables

The server also gets its database's details as environment variables at its next start: `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`, and `DATABASE_URL` (like `mysql://user:password@host:3306/name`). A Redis database gives `REDIS_HOST`, `REDIS_PORT`, `REDIS_USER`, `REDIS_PASSWORD`, and `REDIS_URL`; a MongoDB one `MONGODB_HOST`, `MONGODB_PORT`, `MONGODB_DATABASE`, `MONGODB_USER`, `MONGODB_PASSWORD`, and `MONGODB_URI`. With more than one database, each also gets them prefixed with its name (`LUCKPERMS_DB_HOST`), and the first one's are also the plain `DB_HOST` and the rest. Apps and bots that read their settings from the environment pick them up with nothing to edit.

## Passwords

**Password** shows a database's password; it asks for your passkey first. The key button gives the database a **new password**: the old one stops working at once, and the server gets the new one at its next start. Change it in any config files too.

## Browse and change a database

**Open** on a database shows its tables on the left, and:

- **Rows:** a table's rows, a page at a time. Click a column's name to sort by it, and filter on any column ("contains", "is", "is NULL", and the rest). Click a row to see all of it; with **Change databases** you can edit it there, **Insert row**, or tick rows and delete them. A table without a primary key can only be changed with SQL, since there'd be no safe way to say which row.
- **Structure:** its columns and indexes. Add or drop columns and indexes, rename or drop the table, or make a **New table**; each shows the SQL it runs first.
- **SQL:** write any SQL and run it with **Run** or Ctrl/⌘ + Enter. Table and column names are suggested as you type. With several statements, the last one's result shows. Rows show as they arrive, 200 to a page, and **Cancel** stops the query on the database itself. A query stops after 30 seconds unless you set another limit (up to 600, ten minutes). A result shows its first 10,000 rows and says how many it had; **Export the whole result** writes every row to a file (below).
- **Processes:** what's connected to the database right now, like the server's own plugins, and stopping a slow query.

Someone with only **See databases** can look at everything and run queries that don't change anything: one statement at a time, as a user that can only read.

Changes are in the server's activity, with how many rows they changed, never the SQL itself.

## Import and export

From an opened database (needs **Change databases**):

- **Export** writes the whole database, or the table you're looking at, to the server's `database-exports` folder: as `.sql.gz`, or a table as `.csv`. **Open the folder** takes you there in the file manager to download it.
- **Import** runs a `.sql` or `.sql.gz` file from the server's files against the database. Upload it in the file manager first, then give its path. It stops at the first error and says what it was; what ran before stays.
- On the **SQL** tab, **CSV** saves the rows on screen. When a result has more than fit on screen, **Export the whole result** runs the query again, read-only, and writes every row to `database-exports` as `.csv`.

A dump from another host may make or switch databases, set owners, or name a `DEFINER`; the database's user can't do those, so the import stops there. Remove those lines, or export without them (`--no-owner` for PostgreSQL).

## A read-only user

For a tool that should only look (a stats page, a web map, a backup script), give the database a **read-only user**: **Make one** under the database. It reads every table, including ones made later, and changes nothing. Its **Password** asks for your passkey; the key button gives it a new one; the bin removes it. It reaches the database from the node's servers on the same network, like the database's own user.

## Restore a database from a backup

The server's [backups](/servers/backups/#databases-in-backups) hold its databases. To put one database back as it was, without touching the server's files, click the clock button next to it (**Restore from a backup**, needs **Change databases**) and pick a backup. It asks for your passkey.

What the database has now is exported to the server's `database-exports` folder first, so you can import it again if you picked the wrong one. The database is then emptied and filled from the backup; its user, password, and read-only user stay the same.

## Who can do what

Two permissions on a server's [access](/team/members/) cover its databases: **See databases** shows the tab and the connection details, and **Change databases** lets someone make and delete databases and see and change their passwords. Deleting a database deletes everything in it, and restoring one replaces everything in it, so both ask for a passkey.
