import { useQuery } from "@tanstack/react-query";

import { dbCommand } from "@/lib/db-browser";

// What Wings' key and document browsers return (internal/wings/databases
// keys.go and documents.go): a Redis database's keys and a MongoDB
// database's collections and documents.

export type KeyInfo = { key: string; hex?: string; type: string; ttl: number };
export type KeysResult = { keys: KeyInfo[]; cursor: string; total: number };

export type KeyItem = {
  field?: string;
  index?: number;
  value: string;
  score?: number;
  fields?: Record<string, string>;
};

export type KeyValue = {
  key: string;
  hex?: string;
  type: string;
  ttl: number;
  length: number;
  string?: string;
  items?: KeyItem[];
  cursor?: string;
  cut?: boolean;
};

export type KeyRef = { key: string; hex?: string };

export type CommandResult = { reply: unknown; cut?: boolean; millis: number };

export type Collection = {
  name: string;
  count: number;
  indexes: { name: string; keys: string; unique?: boolean }[];
};

export type DocumentsResult = {
  documents: string[]; // relaxed Extended JSON
  count: number; // -1: counting ran out of time
  more?: boolean;
  cut?: boolean;
  millis: number;
};

export const dbDocumentsCapability = "db-documents";

export const keysKey = (nodeId: string, serverId: string, id: string) => [
  "db-keys",
  nodeId,
  serverId,
  id,
];
export const collectionsKey = (nodeId: string, serverId: string, id: string) => [
  "db-collections",
  nodeId,
  serverId,
  id,
];

export function useCollections(nodeId: string, serverId: string, id: string) {
  return useQuery({
    queryKey: collectionsKey(nodeId, serverId, id),
    queryFn: async () =>
      (
        await dbCommand<{ collections: Collection[] }>(nodeId, serverId, "db.collections", {
          id,
        })
      ).collections,
    retry: false,
  });
}

// docID is a document's _id as Extended JSON, for replacing or deleting it.
export function docID(doc: string): string {
  try {
    return JSON.stringify((JSON.parse(doc) as { _id?: unknown })._id ?? null);
  } catch {
    return "null";
  }
}

// pretty indents a document for reading.
export function pretty(doc: string): string {
  try {
    return JSON.stringify(JSON.parse(doc), null, 2);
  } catch {
    return doc;
  }
}

// refOf is how commands name a key.
export const refOf = (k: { key: string; hex?: string }): KeyRef =>
  k.hex ? { key: k.key, hex: k.hex } : { key: k.key };
