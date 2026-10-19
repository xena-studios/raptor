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

## Who can do what

Two permissions on a server's [access](/team/members/) cover its databases: **See databases** shows the tab and the connection details, and **Change databases** lets someone make and delete databases and see and change their passwords. Deleting a database deletes everything in it, so it asks for a passkey.
