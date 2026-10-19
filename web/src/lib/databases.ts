import { useQuery } from "@tanstack/react-query";

import { commandClient } from "@/lib/transport";

// What Wings' database commands return (internal/wings/databases,
// docs/DATABASES.md): instances on a node, and servers' databases on them.

export type Engine = "mariadb" | "mysql" | "postgres";

export type DBInstance = {
  id: string;
  name: string;
  engine: Engine;
  version: string;
  image: string;
  networks: string[]; // the owner's networks; [] is the default network
  limits: { memory_mib: number; cpu_percent?: number; disk_mib?: number };
  settings: { max_connections?: number; timezone?: string };
  desired_state: "running" | "stopped";
  created_at: number;
  state: "starting" | "running" | "stopped" | "failed";
  error?: string;
  host: string;
  port: number;
  databases: number;
  addresses: Record<string, string>;
};

export type DBVersion = { engine: Engine; version: string; image: string; default?: boolean };

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
      q.state.data?.instances.some((i) => i.state === "starting") ? 3_000 : 30_000,
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
];

export function engineName(e: Engine): string {
  return engines.find((x) => x.id === e)?.name ?? e;
}

// connectionURL is a database's URL without its password, for showing.
export function connectionURL(d: Database, password = "…"): string {
  const scheme = d.engine === "postgres" ? "postgres" : "mysql";
  return `${scheme}://${d.user}:${password}@${d.host}:${d.port}/${d.name}`;
}

// The labels Wings takes: lowercase letters, digits, and underscores.
export const labelPattern = /^[a-z][a-z0-9_]{0,17}$/;
