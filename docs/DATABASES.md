# Databases

Managed SQL databases on owners' own nodes, with a browser for them in the app: MariaDB, MySQL, and PostgreSQL first, then Redis and MongoDB. Included in the node's price, never hosted by Raptor (DECISIONS #296–#299). This is the plan; nothing here is built yet ([ROADMAP.md](ROADMAP.md#databases)).

## The model

- **Instance:** one database server on a node, run by Wings as a container: an engine (`mariadb`, `mysql`, `postgres`), a version, a name ("Network A MariaDB"), its limits (memory, CPU, disk), its settings, and who may use it. A node can have **any number**, of any engines and versions side by side.
- **Database:** one database on an instance, for one server, with its own user. A server can have several, on several instances.
- **Allowed servers:** each instance lists which of the node's servers can have databases on it ("every server", or chosen ones). This is how two communities on one node are kept apart: each gets its own instance, allowed only for its servers.

Instances aren't eggs or servers: they don't show in the server list, and their lifecycle (create, upgrade, delete) is Wings' own, with the same jobs, backups, and disk limits as servers.

## Isolation

Three layers, so one failing still leaves the others:

1. **Separate instances.** Data that must stay apart goes on separate instances. Nothing in Raptor puts two groups' databases on one instance unless an owner allows both groups' servers on it.
2. **Separate networks.** Each instance has its own Docker network, `raptor-db-<id>`, created `internal` (no route out to the internet). A server's container joins an instance's network only while it has a database there, and leaves it when the last one is deleted. A server with no database on an instance has no route to it at all; the address doesn't exist from its side.
3. **Separate users.** Every database has its own user with rights to that database only:
   - MariaDB and MySQL: `GRANT ALL ON db.* TO 'u'@'%'`, nothing global, no `GRANT OPTION`, no `FILE` or `SUPER`; the root password stays on the node and is never given out.
   - PostgreSQL: the database is owned by its user; `REVOKE CONNECT ON DATABASE … FROM PUBLIC`; the `public` schema's create right revoked from `PUBLIC`; `pg_hba.conf` lets each user reach only its own database (`host db u … scram-sha-256`).

Raptor's nftables table also drops traffic to an instance's address from anything but the containers on its network (`inet raptor`, beside the rules it has today), so a container that found its way onto the bridge some other way still can't connect.

### Server groups (later)

Game servers on a node share one bridge today and can reach each other's ports (RCON, query), which proxies like Velocity rely on. For owners running two separate communities on one machine, **server groups** would give each group its own bridge: servers reach the servers in their group and nothing else, and a group's instances are allowed for its servers by default. That's a change to how servers are networked, so it comes after databases, as its own phase.

## Engines and versions

| Engine | Versions offered | Image |
|---|---|---|
| MariaDB | 10.6, 10.11, 11.4, 11.8 (the LTS releases still supported) | `mariadb:<version>` |
| MySQL | 8.0, 8.4 | `mysql:<version>` |
| PostgreSQL | 14, 15, 16, 17, 18 | `postgres:<version>` |

The list is in the Wings binary (`internal/wings/databases/versions.go`), each version pinned to an image digest that's updated with Wings releases, so a version means the same bits on every node. An instance tracks a **version line** (11.4), not a patch: Wings moves it to the newest patch in its line when an update brings one, by restarting the container on the new image, at the time the owner picks for maintenance.

### Changing an instance's version

From the instance's page, pick another version. Wings decides how:

- **Within a version line, or a newer minor of the same major** (MariaDB 11.4 → 11.8, MySQL 8.0 → 8.4): in place. Wings takes a backup (a dump of every database), stops the instance, starts the new image on the same data, and runs the engine's upgrade step (`mariadb-upgrade`, MySQL's own on start). If it doesn't come up healthy, it's put back on the old image and the old data restored from the backup.
- **A new PostgreSQL major** (16 → 17), whose data files don't carry over: a migration job. Wings dumps every database, starts the new version on a new data directory, restores the dump, checks every database is there with the same tables and row counts, then switches the instance's address to the new one. The old data directory is kept for 7 days, and switching back is one click until then.
- **Going back** to an older version is only by restoring the pre-change backup onto it, since newer data files can't be read by older versions. The app says so before you change.

Each change is a job with its progress shown in the app; servers using the instance lose their connection while it switches (usually under a minute for in-place changes), and the app says which servers that is.

### Settings

Each instance has:

- **Limits:** memory (required, and used to size the engine's caches), CPU, and disk, the disk enforced by an XFS project quota like a server's.
- **Tuning:** the settings owners change most, as fields: max connections, the buffer pool or shared buffers (sized from the memory limit unless set), slow-query logging, the character set and collation (MariaDB/MySQL), and time zone.
- **Advanced:** any other engine setting as `name = value` lines, checked against a list of what's safe to set (no file paths, no plugins that load code, no turning off authentication), written to the instance's config file.
- **Maintenance window:** when patch updates and planned restarts may happen.
- **External access:** off by default. On, the instance gets a port on the node and a firewall rule allowing only the addresses or ranges listed, for connecting from a desktop tool or an outside app. TLS is on for external connections, with a certificate Wings makes for the instance.

## Using a database from a server

Creating a database on a server's **Databases** tab gives it:

- a name (`s<short id>_<name>`), a user, and a random password, made on the node;
- connection details in the tab, and as **environment variables** for the server's next start: `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`, and `DATABASE_URL` (`mysql://…` or `postgres://…`); with more than one database, each also gets them with its name as a prefix (`LUCKPERMS_DB_HOST`…);
- a host name that resolves inside the server's container, `<instance name>.db.raptor`, so configs don't hold an IP that could change.

The password lives on the node (encrypted in `state.db`), never in the Panel. The tab shows it once when the database is made; after that, **Show password** fetches it from the node with a re-auth, and **Reset password** makes a new one.

## The browser

Each database opens in the server's Databases tab, and an instance's whole contents on its own page (admins). Everything goes through typed commands to Wings (`db.schema`, `db.rows`, `db.query`, `db.row.update`…), which connects to the instance locally as that database's user (an instance page uses the instance's admin user), so the browser can never reach further than the database's own rights.

- **Schema:** tables, views, and their columns, types, keys, and indexes; each table's row count and size.
- **Rows:** paged (100 at a time, up to 10,000 per page with a setting), sorted, and filtered by column, with JSON and long text opened in the editor. **Edit, insert, and delete** rows in place; a table without a primary key is read-only in the grid (there'd be no safe way to say which row).
- **SQL editor:** the file manager's editor with SQL highlighting and completion from the schema. Results in a grid, with how many rows and how long; a statement that changes data says how many rows it changed. Every statement is allowed, including DDL: the database's user can only touch its own database anyway. Each run has a time limit (30 s by default, up to 10 minutes) and a result cap (10,000 rows), and can be cancelled.
- **Structure:** create, alter, and drop tables, columns, and indexes from forms, shown as the SQL they'll run before running it.
- **Users** (instance page): the instance's users and what each can reach, and adding a read-only user for an outside tool.
- **Import and export:** upload a `.sql` (or `.sql.gz`) file to run against the database, as a job with progress; download a dump of a database or a table as `.sql`, or a table or a query's result as CSV.
- **Processes:** what's running on the instance now, and killing a query.

Results travel through the Panel like the file manager's do (encrypted, not stored). Queries that change something are recorded in the server's activity with who ran them and how many rows they changed, but not their text, which can hold data.

## Permissions

- **See databases:** the Databases tab, connection details (without the password), the schema, rows, and read-only queries.
- **Change databases:** everything in the browser: editing rows, any SQL, structure, imports, and showing or resetting the password.
- Admins and owners: creating and deleting instances, changing their versions and settings, external access, and which servers may use them; creating databases for any server.
- **Signed with a passkey:** deleting a database or an instance, restoring a database over itself, and turning on external access (it opens a port).

## Backups

- A server's backups include its databases: before the files are snapshotted, Wings dumps each one (`mariadb-dump --single-transaction`, `mysqldump --single-transaction`, `pg_dump -Fc`) into the snapshot, so it's consistent and restorable with the server. **Restore everything** restores them too; restoring a single database from a backup is on the database's own menu.
- An instance can also be backed up whole on its own schedule (every database on it), to any of the node's destinations.
- Version changes always take a backup first (above).

## Health and alerts

- Each instance's state (running, starting, stopped, upgrading, failed), uptime, connections, queries per second, and size, sampled with the node's metrics.
- New alert conditions: an instance down or restarting, near its disk limit, near its connection limit, and a version change finished or failed.
- `raptor doctor` checks each instance is running and reachable on its network, and `raptor db ps` on the node lists them.

## Phases

1. **Instances and databases (MariaDB, MySQL, PostgreSQL):** creating instances with any version, limits and tuning; allowed servers; per-instance networks and the firewall rule; creating databases and users for servers; connection details, environment variables, and host names; passwords on the node; the Databases tab and the node's Databases page; capability `databases`.
2. **The browser:** schema, rows with editing, the SQL editor, structure forms, processes.
3. **Import, export, and users:** `.sql` imports as jobs, dumps and CSV exports, read-only users.
4. **Versions and backups:** patch updates in the maintenance window, in-place and migration upgrades with rollback, databases in server backups, instance backups.
5. **External access:** the port, the address allowlist, TLS.
6. **Redis and MongoDB:** one instance per server for Redis (it has no real per-database isolation), shared instances with per-database users for MongoDB; a key browser and a document browser.
7. **Server groups:** a bridge per group, so separate communities on one node can't reach each other's servers either.
