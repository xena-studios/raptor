import { useQuery, useQueryClient } from "@tanstack/react-query";

import { commandClient } from "@/lib/transport";

// What Wings' files actions return (internal/wings/files).
export type Entry = {
  name: string;
  type: "file" | "dir" | "symlink" | "other";
  size: number;
  mode: number;
  modified_at: number;
  target?: string;
  target_type?: string;
  denied?: boolean;
};
type Listing = { path: string; entries: Entry[]; truncated?: boolean };
export type Content = Entry & { data: string };

// kind is what an entry acts as: a link acts as what it points to.
export function kind(e: Entry): Entry["type"] {
  return e.type === "symlink" ? ((e.target_type as Entry["type"]) ?? "other") : e.type;
}

// sorted puts folders first, then names in the order people expect
// (case-insensitive, numbers by value).
export function sorted(entries: Entry[]): Entry[] {
  const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
  return [...entries].sort((a, b) => {
    const da = kind(a) === "dir" ? 0 : 1;
    const db = kind(b) === "dir" ? 0 : 1;
    return da - db || collator.compare(a.name, b.name);
  });
}

// FilesApi runs file actions, and names their queries.
export type FilesApi = {
  run: <T>(action: string, params: object) => Promise<T | undefined>;
  listKey: (path: string) => readonly string[];
  contentKey: (path: string) => readonly string[];
  refresh: () => Promise<void>;
};

// useFilesApi runs file actions on one server and keeps its listings.
// support: as Raptor's staff, under that support request.
export function useFilesApi(nodeId: string, serverId: string, support?: string): FilesApi {
  const client = useQueryClient();
  async function run<T>(action: string, params: object): Promise<T | undefined> {
    const res = await commandClient.execute({
      nodeId,
      action,
      serverId,
      paramsJson: JSON.stringify(params),
      support,
    });
    return res.resultJson ? (JSON.parse(res.resultJson) as T) : undefined;
  }
  return {
    run,
    listKey: (path: string) => ["files", nodeId, serverId, path] as const,
    contentKey: (path: string) => ["file", nodeId, serverId, path] as const,
    refresh: () => client.invalidateQueries({ queryKey: ["files", nodeId, serverId] }),
  };
}

// useBackupFilesApi is useFilesApi for a backup's files, read-only: listing
// a folder is backup.browse, reading a file backup.read.
export function useBackupFilesApi(nodeId: string, serverId: string, backupId: string): FilesApi {
  const client = useQueryClient();
  const actions: Record<string, string> = {
    "files.list": "backup.browse",
    "files.read": "backup.read",
  };
  async function run<T>(action: string, params: object): Promise<T | undefined> {
    const res = await commandClient.execute({
      nodeId,
      action: actions[action] ?? action,
      serverId,
      paramsJson: JSON.stringify({ ...params, backup_id: backupId }),
    });
    return res.resultJson ? (JSON.parse(res.resultJson) as T) : undefined;
  }
  return {
    run,
    listKey: (path: string) => ["backup-files", nodeId, serverId, backupId, path] as const,
    contentKey: (path: string) => ["backup-file", nodeId, serverId, backupId, path] as const,
    refresh: () =>
      client.invalidateQueries({ queryKey: ["backup-files", nodeId, serverId, backupId] }),
  };
}

export function useListing(api: FilesApi, path: string, enabled = true) {
  return useQuery({
    queryKey: api.listKey(path),
    queryFn: () => api.run<Listing>("files.list", { path }),
    enabled,
    staleTime: 5_000,
  });
}
