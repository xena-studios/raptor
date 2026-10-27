import { useQuery } from "@tanstack/react-query";

import { commandClient } from "@/lib/transport";

export { versionChange } from "./db-versions";

// What Wings' database commands return (internal/wings/databases,
// docs/DATABASES.md): instances on a node, and servers' databases on them.

export type Engine = "mariadb" | "mysql" | "postgres" | "redis" | "valkey" | "mongodb";

// isSQL: the engines with the SQL browser, imports of .sql files, and
// external access.
export const isSQL = (e: Engine) => e === "mariadb" || e === "mysql" || e === "postgres";

// isRedis: Redis and Valkey, its fork, which work the same here (one
// server each, the key browser, REDIS_* variables).
export const isRedis = (e: Engine) => e === "redis" || e === "valkey";

export type DBInstance = {
  id: string;
  name: string;
  engine: Engine;
  version: string;
  image: string;
  networks: string[]; // the owner's networks; [] is the default network
  limits: { memory_mib: number; cpu_percent?: number; disk_mib?: number };
  settings: {
    max_connections?: number;
    timezone?: string;
    maintenance?: string;
    advanced?: string; // name = value lines (db-settings.ts)
  };
  desired_state: "running" | "stopped";
  created_at: number;
  state: "starting" | "running" | "stopped" | "failed" | "upgrading";
  error?: string;
  host: string;
  port: number;
  databases: number;
  addresses: Record<string, string>;
  update?: boolean; // a newer patch of its version is in this Wings
  previous?: string; // the version it can go back to, until go_back_until
  go_back_until?: number; // unix seconds
  change?: DBChange;
  external?: DBExternal; // its port on the node, if it has external access
};

// DBExternal is an instance's external access: a port on the node that
// the addresses in allow reach, with TLS checked against ca.
export type DBExternal = {
  port: number;
  allow: string[];
  host?: string; // the node's name in DNS, which the certificate names
  ca?: string; // PEM
};

// DBChange is an instance's version change, going on or the last one.
export type DBChange = {
  job_id: string;
  kind: "version" | "patch" | "back";
  from: string;
  to: string;
  step?: string;
  done?: boolean;
  error?: string; // it was put back as it was
};

export type DBVersion = {
  engine: Engine;
  version: string;
  image: string;
  default?: boolean;
  unavailable?: string; // why this node can't run it
};

export type Database = {
  id: string;
  instance_id: string;
  server_id: string;
  label: string;
  name: string;
  user: string;
  engine: Engine;
  host: string;
  port: number;
  created_at: number;
  readonly_user?: string; // its read-only user, if it has one
};

// What node.db.instance.save takes: no ID creates one.
export type InstanceInput = {
  id?: string;
  name: string;
  engine?: Engine;
  version?: string;
  networks: string[];
  limits: DBInstance["limits"];
  settings: DBInstance["settings"];
};

export const databasesCapability = "databases";
// Version changes, going back, and patch updates in the maintenance window.
export const dbVersionsCapability = "db-versions";
// Servers' backups hold their databases, and one restores alone.
export const dbBackupsCapability = "db-backups";

// External access: an instance's port on the node, with an allowlist
// and TLS.
export const dbExternalCapability = "db-external";

// Advanced engine settings: name = value lines on each engine's safe list.
export const dbSettingsCapability = "db-settings";

// External access for Redis and Valkey, on a TLS-only port.
export const dbRedisExternalCapability = "db-redis-external";

// The maintenance window an instance gets unless it says (Wings'
// DefaultMaintenance), in the node's time.
export const defaultMaintenance = "04:00";

export const instancesKey = (nodeId: string) => ["db-instances", nodeId];
export const serverDatabasesKey = (nodeId: string, serverId: string) => [
  "server-databases",
  nodeId,
  serverId,
];

// useInstances is a node's database instances, refreshed while any is
// starting.
export function useInstances(nodeId: string, enabled = true) {
  return useQuery({
    queryKey: instancesKey(nodeId),
    queryFn: async () => {
      const res = await commandClient.execute({
        nodeId,
        action: "node.db.instances",
        paramsJson: "{}",
      });
      return JSON.parse(res.resultJson) as { instances: DBInstance[]; versions: DBVersion[] };
    },
    enabled,
    retry: false,
    refetchInterval: (q) =>
      q.state.data?.instances.some(
        (i) => i.state === "starting" || i.state === "upgrading" || (i.change && !i.change.done),
      )
        ? 3_000
        : 30_000,
  });
}

// useServerDatabases is a server's databases, and the instances it can
// have them on.
export function useServerDatabases(nodeId: string, serverId: string, enabled = true) {
  return useQuery({
    queryKey: serverDatabasesKey(nodeId, serverId),
    queryFn: async () => {
      const res = await commandClient.execute({
        nodeId,
        serverId,
        action: "db.list",
        paramsJson: "{}",
      });
      return JSON.parse(res.resultJson) as { databases: Database[]; instances: DBInstance[] };
    },
    enabled,
    retry: false,
  });
}

export const engines: { id: Engine; name: string; about: string }[] = [
  {
    id: "mariadb",
    name: "MariaDB",
    about: "What most Minecraft plugins expect (LuckPerms, CoreProtect).",
  },
  { id: "mysql", name: "MySQL", about: "For software that asks for MySQL itself." },
  { id: "postgres", name: "PostgreSQL", about: "For apps and bots built on Postgres." },
  {
    id: "redis",
    name: "Redis",
    about: "Fast storage for caches and plugins that sync servers. One server each.",
  },
  {
    id: "valkey",
    name: "Valkey",
    about: "Redis' open-source fork, for the same plugins. One server each.",
  },
  { id: "mongodb", name: "MongoDB", about: "For bots and apps that store documents." },
];

export function engineName(e: Engine): string {
  return engines.find((x) => x.id === e)?.name ?? e;
}

// connectionURL is a database's URL without its password, for showing.
export function connectionURL(d: Database, password = "…"): string {
  switch (d.engine) {
    case "redis":
    case "valkey":
      return `redis://${d.user}:${password}@${d.host}:${d.port}/0`;
    case "mongodb":
      return `mongodb://${d.user}:${password}@${d.host}:${d.port}/${d.name}?authSource=${d.name}`;
    case "postgres":
      return `postgres://${d.user}:${password}@${d.host}:${d.port}/${d.name}`;
  }
  return `mysql://${d.user}:${password}@${d.host}:${d.port}/${d.name}`;
}

// hostVariable is the variable a server gets a database's host in, and
// urlVariable its URL's (Wings' envVars).
export function hostVariable(e: Engine): string {
  return isRedis(e) ? "REDIS_HOST" : e === "mongodb" ? "MONGODB_HOST" : "DB_HOST";
}
export function urlVariable(e: Engine): string {
  return isRedis(e) ? "REDIS_URL" : e === "mongodb" ? "MONGODB_URI" : "DATABASE_URL";
}

// The labels Wings takes: lowercase letters, digits, and underscores.
export const labelPattern = /^[a-z][a-z0-9_]{0,17}$/;

// downloadCA saves an instance's certificate authority, for clients that
// check the instance's certificate.
export function downloadCA(instance: DBInstance) {
  if (!instance.external?.ca) return;
  const url = URL.createObjectURL(
    new Blob([instance.external.ca], { type: "application/x-pem-file" }),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = `${instance.host.replace(/\.db\.raptor$/, "")}-ca.pem`;
  a.click();
  URL.revokeObjectURL(url);
}
