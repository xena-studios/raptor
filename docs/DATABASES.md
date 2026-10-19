# Databases

Managed SQL databases on owners' own nodes, with a browser for them in the app: MariaDB, MySQL, and PostgreSQL first, then Redis and MongoDB. Included in the node's price, never hosted by Raptor (DECISIONS #296–#299). They run on the node's [networks](NETWORKS.md), built first. Phases 1 (instances and databases), 2 (the browser), and 3 (import, export, and read-only users) are built; the rest is the plan ([ROADMAP.md](ROADMAP.md#networks-and-databases), DECISIONS #305–#308).

## How phase 1 is built

- `internal/wings/databases`: instances in `database_instances` (engine, version line, image digest, sealed root password, networks, limits, settings), databases in `databases`, instances' fixed addresses in `instance_addresses`. A server's databases have no foreign key to `servers`: they're dropped on their instance first, when the server is deleted (`ServerDeleted`) or at the next start, then removed.
- **Containers** are services (`EnsureService`): created from the spec, created again when its hash (label `raptor.wings.spec`) changes, restarted by Docker (`unless-stopped`), role `database`, a read-only root with `/tmp` in memory, Docker's default seccomp profile, and the memory and CPU limits. Files are in `<volumes>/databases/<id>/{data,run,conf}` on the server data volume, under their own quota project (from the servers' sequence).
- **Wings reaches an instance only through its socket** in `run/`, never the network. MariaDB and MySQL's root is `localhost` only (`*_ROOT_HOST=localhost`); PostgreSQL's `pg_hba.conf` (Wings', in `conf/`, read-only to the engine) is `local all all scram-sha-256`, `host all postgres all reject`, `host sameuser all all scram-sha-256`: a database and its user share a name, so a user can only ever connect to its own database.
- **Names:** a database and its user are `s<the server's short ID>_<name>`, where the short ID is the last 12 characters of its ID (as the app shows it) and the name is up to 18 characters, inside MySQL's 32-character user names. The instance is `<name>.db.raptor` and `<short id>.db.raptor` on its networks.
- **Passwords** are 32 letters and digits, sealed with AES-256-GCM under the node's database key (`databases.key` beside `state.db`, root only).
- **The guard** (`firewall.Guard`): on each of the owner's networks, the instance's fixed address drops connections from any address but the fixed addresses of servers with a database on it, and its IPv6 address (Docker's) refuses them all so clients fall back to IPv4 at once. It's in `forward` before the jumps between networks, so a rule between networks can't reach an instance either; it needs `br_netfilter` with `bridge-nf-call-iptables` on, which Docker sets and Wings makes sure of. On the default network there's no guard: every server there reaches the instance, and only users keep databases apart.
- **Tuning:** the buffer pool or shared buffers from the memory limit (half for InnoDB, a quarter for PostgreSQL), `max_connections` (100), `utf8mb4` with `utf8mb4_unicode_ci`, the performance schema off under 1 GiB (it alone takes hundreds of MB on MySQL), `secure-file-priv=NULL` on MySQL, and the time zone (the node's by default). The free-form Advanced settings come later.
- **Commands:** `node.db.instances` (a read, with the versions), `node.db.instance.save`, `node.db.instance.power`, `node.db.instance.delete` (signed), and for a server `db.list`, `db.create` (returns the password once), `db.delete` (signed), `db.password` (signed), `db.password.reset`. Permissions `databases.read` and `databases.write` (which includes it); the node's are admin-only. Capability `databases`.
- **Tested** against the real engines (`internal/wings/databases`, e2e): each engine starts, a user reaches its own database over the network and not another server's, root can't log in over the network with its password, a new password replaces the old, a restart keeps the data, and a deleted database's user is gone. `TestRuntimeDatabaseGuard` checks the guard: a server with a database reaches its instance, another on the same network doesn't, and the machine always does.

## The model

- **Instance:** one database server on a node, run by Wings as a container: an engine (`mariadb`, `mysql`, `postgres`), a version, a name ("Network A MariaDB"), its limits (memory, CPU, disk), its settings, and who may use it. A node can have **any number**, of any engines and versions side by side.
- **Database:** one database on an instance, for one server, with its own user. A server can have several, on several instances.
- **Networks:** an instance joins one or more of the node's [networks](NETWORKS.md), and only servers on those networks can have databases on it or reach it. This is how two communities on one node are kept apart: each gets its own network and its own instance on it.

Instances aren't eggs or servers: they don't show in the server list, and their lifecycle (create, upgrade, delete) is Wings' own, with the same jobs, backups, and disk limits as servers.

## Isolation

Three layers, so one failing still leaves the others:

1. **Separate instances.** Data that must stay apart goes on separate instances. Nothing in Raptor puts two groups' databases on one instance unless an owner allows both groups' servers on it.
2. **Separate networks.** An instance is reachable only on the [networks](NETWORKS.md) it's joined to, and networks don't reach each other unless an owner adds a rule. Inside a network, the instance's address answers only from servers with a database on it (Raptor's firewall drops the rest), so a server that shares a network but has no database there still can't connect.
3. **Separate users.** Every database has its own user with rights to that database only:
   - MariaDB and MySQL: `GRANT ALL ON db.* TO 'u'@'%'`, nothing global, no `GRANT OPTION`, no `FILE` or `SUPER`; the root password stays on the node and is never given out.
   - PostgreSQL: the database is owned by its user; `REVOKE CONNECT ON DATABASE … FROM PUBLIC`; the `public` schema's create right revoked from `PUBLIC`; `pg_hba.conf` lets each user reach only its own database (`host db u … scram-sha-256`).


Separating game servers themselves (so one community's servers can't reach another's either) is the same networks: see [NETWORKS.md](NETWORKS.md).

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

- a name (`s<short id>_<name>`, the server's short ID being the last part of its ID), a user with the same name, and a random password, made on the node;
- connection details in the tab, and as **environment variables** for the server's next start: `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASSWORD`, and `DATABASE_URL` (`mysql://…` or `postgres://…`); with more than one database, each also gets them with its name as a prefix (`LUCKPERMS_DB_HOST`…);
- a host name that resolves on the instance's networks, `<instance name>.db.raptor`, so configs don't hold an IP that could change.

The password lives on the node (encrypted in `state.db`), never in the Panel. The tab shows it once when the database is made; after that, **Show password** fetches it from the node with a re-auth, and **Reset password** makes a new one.

## The browser

### How phase 2 is built

- `internal/wings/databases/browser.go`: every command opens a connection **as the database's own user**, through the instance's socket, with its sealed password, so nothing in the browser reaches further than that user can. Table and column names from the app are only used once found in the live schema, then quoted; values are always parameters (PostgreSQL's sent as literals the engine casts to the column's type, so a string can go into any column).
- **Rows** are paged by `LIMIT`/`OFFSET` (one more than asked, to know there's more), ordered by the primary key unless sorted, with filters (`=`, `<>`, `<`, `<=`, `>`, `>=`, contains, NULL, not NULL); row counts are the engine's estimates. **Edits** go by the whole primary key in one transaction, and an update or delete that doesn't match exactly the rows asked for changes nothing; a table without a primary key is read-only in the grid. Values over 8 KB arrive cut and can't be edited in the grid.
- **Queries:** those with **Change databases** run any SQL (`db.query`): on PostgreSQL every statement's result is read and the last shown, with its rows changed; on MariaDB and MySQL statements that return rows (by their first word) are read and the rest executed, the last one's count shown. Those with only **See databases** run `db.query.read`: **one statement** (MariaDB and MySQL's connection takes no more; PostgreSQL's extended protocol neither), in a **read-only transaction**, MariaDB and MySQL's session read-only too, rolled back after. The e2e test tries `DELETE`, `DROP`, `CREATE`, `UPDATE`, and a second statement after a `SELECT` or a `COMMIT` on every engine; all fail.
- **Limits, for now:** a result is at most 1,000 rows and 2 MB, a value at most 8 KB shown; a query runs for 30 seconds by default and at most 50 (a command has a minute through the Panel), and one past its limit is killed on the server (MariaDB and MySQL's `KILL QUERY` as root, PostgreSQL's cancel and `statement_timeout`). Longer queries, a cancel button, and pages up to 10,000 rows need results streamed like file transfers; they're for later.
- **Structure** forms (create table with an auto-numbered `id`, add and drop columns, add and drop indexes, rename and drop a table) write the SQL in the app, show it, and run it as `db.query`.
- **Processes** are the connections the database's user can see (its own), with their running query, and stopping one (`KILL QUERY`, `pg_cancel_backend`).
- **Activity:** `db.query`, the row edits, and stopping a query are recorded like any change, with `rows_changed` from their result and never their SQL (the Panel doesn't keep commands' params). Reads aren't recorded.
- Commands: `db.schema`, `db.rows`, `db.query.read`, `db.processes` (See databases), `db.query`, `db.row.insert`, `db.row.update`, `db.row.delete`, `db.process.kill` (Change databases); none signed. Capability `db-browser`.
- The instance page's own browser (admins, as the instance's administrator) isn't built; the server's databases are.

### How phase 3 is built

- **Through the server's files**, which already move big files both ways (DECISIONS #311): an import runs a `.sql` or `.sql.gz` file the server has (gzip known by its first bytes); an export writes `database-exports/<name>[-<table>]-<UTC time>.sql.gz` (or `.csv`) into the server's files, through a `.part` file renamed when it's done, for the file manager to download. Both are jobs (`db.import`, `db.export`, in the files class), followed with `db.transfer.status` (bytes so far, of how many for an import) and stopped with `db.transfer.cancel`.
- **The engines' own tools**, run in the instance's container as uid 999 with Docker's exec (`Runtime.Exec`), their input and output streamed: nothing is written in the container, and the password goes in the environment (`MYSQL_PWD`, `PGPASSWORD`), never on a command line. As the database's own user, through the socket:
  - MariaDB/MySQL: `mariadb`/`mysql --binary-mode` to import, stopping at the first error; `mariadb-dump`/`mysqldump --single-transaction --no-tablespaces --skip-lock-tables --triggers` (MySQL also `--set-gtid-purged=OFF`) to export, consistent without locks and needing no rights the user lacks.
  - PostgreSQL: `psql --set=ON_ERROR_STOP=1` to import; `pg_dump --no-owner --no-privileges` (with `--table` for one) to export.
  - A failure's error is the tool's line that names it (psql's, not the `^` under it).
- **CSV** of a table is written by Wings itself, streamed row by row through the browser's connection; a query result's CSV is made in the app from the rows on screen.
- **Read-only users:** `r<short id>_<name>` beside `s<short id>_<name>`, made on request (`db.readonly.set`, which gives an existing one a new password), with `SELECT` and `SHOW VIEW` on the database (MariaDB/MySQL), or on PostgreSQL `CONNECT`, `USAGE` and `SELECT` on every schema, its tables and sequences, and the database user's default privileges so tables made later are readable too. PostgreSQL's `pg_hba.conf` gets a line per read-only user for its own database, and is reloaded; removing one (`db.readonly.remove`) drops what it was granted (`DROP OWNED BY`) and the role. Deleting a database removes its read-only user. The password is shown with a passkey, like the database's (`db.password` with `readonly`).
- Importing changes data and exporting gives it out, so both, and read-only users, need **Change databases**. Capability `db-transfers`.
- **Tested** (`TestTransfers`, real engines): a database exported and imported back, a table as CSV (quotes and commas), a broken file failing with the engine's reason, and a read-only user reading tables made before and after it, failing to insert or drop, gone once removed, and gone with its database.
- **Imports from elsewhere:** a dump that makes or switches databases (`CREATE DATABASE`, `USE`), sets owners (`ALTER … OWNER TO`), or has `DEFINER` clauses needs rights the database's user doesn't have, and stops there. Export with `--no-owner` (PostgreSQL) or without those (MariaDB/MySQL).

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

1. **Instances and databases (MariaDB, MySQL, PostgreSQL),** after [networks](NETWORKS.md#phases): creating instances with any version, limits and tuning, on chosen networks; the firewall rule for servers without a database; creating databases and users for servers; connection details, environment variables, and host names; passwords on the node; the Databases tab and the node's Databases page; capability `databases`.
2. **The browser:** schema, rows with editing, the SQL editor, structure forms, processes.
3. **Import, export, and users:** `.sql` imports as jobs, dumps and CSV exports, read-only users.
4. **Versions and backups:** patch updates in the maintenance window, in-place and migration upgrades with rollback, databases in server backups, instance backups.
5. **External access:** the port, the address allowlist, TLS.
6. **Redis and MongoDB:** one instance per server for Redis (it has no real per-database isolation), shared instances with per-database users for MongoDB; a key browser and a document browser.
