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

## Variables

The server also gets its database's details as environment variables at its next start: `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`, and `DATABASE_URL` (like `mysql://user:password@host:3306/name`). With more than one database, each also gets them prefixed with its name (`LUCKPERMS_DB_HOST`), and the first one's are also the plain `DB_HOST` and the rest. Apps and bots that read their settings from the environment pick them up with nothing to edit.

## Passwords

**Password** shows a database's password; it asks for your passkey first. The key button gives the database a **new password**: the old one stops working at once, and the server gets the new one at its next start. Change it in any config files too.

## Browse and change a database

**Open** on a database shows its tables on the left, and:

- **Rows:** a table's rows, a page at a time. Click a column's name to sort by it, and filter on any column ("contains", "is", "is NULL", and the rest). Click a row to see all of it; with **Change databases** you can edit it there, **Insert row**, or tick rows and delete them. A table without a primary key can only be changed with SQL, since there'd be no safe way to say which row.
- **Structure:** its columns and indexes. Add or drop columns and indexes, rename or drop the table, or make a **New table**; each shows the SQL it runs first.
- **SQL:** write any SQL and run it with **Run** or Ctrl/⌘ + Enter. Table and column names are suggested as you type. With several statements, the last one's result shows. A query stops after 30 seconds unless you set another limit (up to 50), and up to 1,000 rows are shown.
- **Processes:** what's connected to the database right now, like the server's own plugins, and stopping a slow query.

Someone with only **See databases** can look at everything and run queries that don't change anything: one statement at a time, read-only.

Changes are in the server's activity, with how many rows they changed, never the SQL itself.

## Import and export

From an opened database (needs **Change databases**):

- **Export** writes the whole database, or the table you're looking at, to the server's `database-exports` folder: as `.sql.gz`, or a table as `.csv`. **Open the folder** takes you there in the file manager to download it.
- **Import** runs a `.sql` or `.sql.gz` file from the server's files against the database. Upload it in the file manager first, then give its path. It stops at the first error and says what it was; what ran before stays.
- On the **SQL** tab, **CSV** saves the result on screen.

A dump from another host may make or switch databases, set owners, or name a `DEFINER`; the database's user can't do those, so the import stops there. Remove those lines, or export without them (`--no-owner` for PostgreSQL).

## A read-only user

For a tool that should only look (a stats page, a web map, a backup script), give the database a **read-only user**: **Make one** under the database. It reads every table, including ones made later, and changes nothing. Its **Password** asks for your passkey; the key button gives it a new one; the bin removes it. It reaches the database from the node's servers on the same network, like the database's own user.

## Who can do what

Two permissions on a server's [access](/team/members/) cover its databases: **See databases** shows the tab and the connection details, and **Change databases** lets someone make and delete databases and see and change their passwords. Deleting a database deletes everything in it, so it asks for a passkey.
