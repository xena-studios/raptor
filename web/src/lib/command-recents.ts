// The command menu's memory (#436): what was picked lately, how often,
// and what's pinned, kept per browser (localStorage). Items are stored by
// id only and looked up again among what the menu can show now, so a
// server you lost access to drops out on its own. Pure apart from the
// storage, which is passed in (and may throw: private windows).

import type { Use } from "./command-search.ts";

export type Memory = {
  // By item id: how often and when it was last picked.
  uses: Record<string, Use>;
  // Pinned item ids, in the order they were pinned.
  pinned: string[];
};

export const storageKey = "raptor.command-menu";

// How many items it remembers uses of; the least recently used go first.
const maxUses = 100;
export const maxPinned = 12;

export function emptyMemory(): Memory {
  return { uses: {}, pinned: [] };
}

type Storage = Pick<globalThis.Storage, "getItem" | "setItem">;

// loadMemory reads the memory, or an empty one if there's none, it's
// unreadable, or storage is off.
export function loadMemory(storage: Storage | undefined): Memory {
  try {
    const raw = storage?.getItem(storageKey);
    if (!raw) return emptyMemory();
    const v = JSON.parse(raw) as Partial<Memory>;
    const uses: Record<string, Use> = {};
    for (const [id, u] of Object.entries(v.uses ?? {})) {
      if (typeof u?.count === "number" && typeof u?.last === "number") uses[id] = u;
    }
    const pinned = Array.isArray(v.pinned)
      ? v.pinned.filter((x): x is string => typeof x === "string").slice(0, maxPinned)
      : [];
    return { uses, pinned };
  } catch {
    return emptyMemory();
  }
}

// saveMemory writes it, quietly doing nothing when storage is off or full.
export function saveMemory(storage: Storage | undefined, m: Memory): void {
  try {
    storage?.setItem(storageKey, JSON.stringify(m));
  } catch {
    // Private browsing or a full quota: it's only remembered for this page.
  }
}

// recordUse is the memory with one more use of id at now.
export function recordUse(m: Memory, id: string, now: number): Memory {
  const prev = m.uses[id];
  const uses = { ...m.uses, [id]: { count: (prev?.count ?? 0) + 1, last: now } };
  const ids = Object.keys(uses);
  if (ids.length > maxUses) {
    ids.sort((a, b) => (uses[a]?.last ?? 0) - (uses[b]?.last ?? 0));
    for (const old of ids.slice(0, ids.length - maxUses)) delete uses[old];
  }
  return { ...m, uses };
}

// togglePin pins id, or unpins it if it's pinned. Past the limit, the
// oldest pin goes.
export function togglePin(m: Memory, id: string): Memory {
  if (m.pinned.includes(id)) return { ...m, pinned: m.pinned.filter((x) => x !== id) };
  return { ...m, pinned: [...m.pinned, id].slice(-maxPinned) };
}

// recentIds is the ids used most lately, newest first, without pinned ones
// (they're listed above).
export function recentIds(m: Memory, limit: number): string[] {
  return Object.entries(m.uses)
    .filter(([id]) => !m.pinned.includes(id))
    .sort((a, b) => b[1].last - a[1].last)
    .slice(0, limit)
    .map(([id]) => id);
}
