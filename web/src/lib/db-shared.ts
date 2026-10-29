// SharedDatabase is one database on an instance, with the servers linked
// to it (node.db.databases).
export type SharedDatabase = {
  id: string;
  instance_id: string;
  label: string;
  db_name: string;
  username: string;
  created_at: number;
  links: { server_id: string; created_at: number }[];
};

// sharedDatabases reads node.db.databases' list. Nodes from before
// databases were shared answer with each database's one server_id (and
// name and user), so that's its one link.
export function sharedDatabases(raw: unknown): SharedDatabase[] {
  const list = (raw as { databases?: Record<string, unknown>[] } | null)?.databases ?? [];
  return list.map((d) => {
    const str = (k: string) => (typeof d[k] === "string" ? (d[k] as string) : "");
    const created = typeof d.created_at === "number" ? d.created_at : 0;
    const links = Array.isArray(d.links)
      ? (d.links as { server_id: string; created_at?: number }[]).map((l) => ({
          server_id: l.server_id,
          created_at: l.created_at ?? 0,
        }))
      : str("server_id")
        ? [{ server_id: str("server_id"), created_at: created }]
        : [];
    return {
      id: str("id"),
      instance_id: str("instance_id"),
      label: str("label"),
      db_name: str("db_name") || str("name"),
      username: str("username") || str("user"),
      created_at: created,
      links,
    };
  });
}

// throughServer is node.db.databases' list for the instance page, which
// opens each database through a server it's linked to (the db.* commands
// are a server's): an older node's server_id, else the first link, else
// none. name and user are filled in from db_name and username.
export function throughServer<T extends { server_id?: string; name?: string; user?: string }>(
  raw: unknown,
): T[] {
  const list = (raw as { databases?: Record<string, unknown>[] } | null)?.databases ?? [];
  const shared = sharedDatabases(raw);
  return list.map((d, i) => {
    const s = shared[i];
    return {
      ...d,
      server_id: (typeof d.server_id === "string" && d.server_id) || s?.links[0]?.server_id || "",
      name: (typeof d.name === "string" && d.name) || s?.db_name || "",
      user: (typeof d.user === "string" && d.user) || s?.username || "",
    } as unknown as T;
  });
}
