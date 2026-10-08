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
export type Listing = { path: string; entries: Entry[]; truncated?: boolean };
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

// useFilesApi runs file actions on one server and keeps its listings.
export function useFilesApi(nodeId: string, serverId: string) {
  const client = useQueryClient();
  async function run<T>(action: string, params: object): Promise<T | undefined> {
    const res = await commandClient.execute({
      nodeId,
      action,
      serverId,
      paramsJson: JSON.stringify(params),
    });
    return res.resultJson ? (JSON.parse(res.resultJson) as T) : undefined;
  }
  return {
    run,
    listKey: (path: string) => ["files", nodeId, serverId, path] as const,
    refresh: () => client.invalidateQueries({ queryKey: ["files", nodeId, serverId] }),
  };
}

export function useListing(api: ReturnType<typeof useFilesApi>, path: string, enabled = true) {
  return useQuery({
    queryKey: api.listKey(path),
    queryFn: () => api.run<Listing>("files.list", { path }),
    enabled,
    staleTime: 5_000,
  });
}
