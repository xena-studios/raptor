import assert from "node:assert/strict";
import { test } from "node:test";

import {
  emptyMemory,
  loadMemory,
  maxPinned,
  recentIds,
  recordUse,
  saveMemory,
  storageKey,
  togglePin,
} from "./command-recents.ts";

function fakeStorage(initial?: string) {
  const data = new Map<string, string>();
  if (initial !== undefined) data.set(storageKey, initial);
  return {
    data,
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => {
      data.set(k, v);
    },
  };
}

const throwing = {
  getItem: (): string | null => {
    throw new Error("SecurityError");
  },
  setItem: () => {
    throw new Error("QuotaExceededError");
  },
};

test("memory survives a round trip through storage", () => {
  const s = fakeStorage();
  const m = togglePin(recordUse(emptyMemory(), "server:a", 5), "page:nodes");
  saveMemory(s, m);
  assert.deepEqual(loadMemory(s), m);
});

test("missing, broken, or blocked storage reads as empty and never throws", () => {
  assert.deepEqual(loadMemory(undefined), emptyMemory());
  assert.deepEqual(loadMemory(fakeStorage("{not json")), emptyMemory());
  assert.deepEqual(loadMemory(throwing), emptyMemory());
  assert.doesNotThrow(() => saveMemory(throwing, emptyMemory()));
  // Junk entries are dropped, good ones kept.
  const s = fakeStorage(
    JSON.stringify({ uses: { a: { count: 1, last: 2 }, b: "x" }, pinned: ["p", 3] }),
  );
  assert.deepEqual(loadMemory(s), { uses: { a: { count: 1, last: 2 } }, pinned: ["p"] });
});

test("recordUse counts uses and forgets the least recent past the limit", () => {
  let m = recordUse(emptyMemory(), "a", 1);
  m = recordUse(m, "a", 2);
  assert.deepEqual(m.uses.a, { count: 2, last: 2 });
  for (let i = 0; i < 120; i++) m = recordUse(m, `x${i}`, 10 + i);
  assert.equal(Object.keys(m.uses).length, 100);
  assert.equal(m.uses.a, undefined, "the oldest went first");
  assert.ok(m.uses.x119);
});

test("togglePin pins, unpins, and keeps the newest past the limit", () => {
  let m = togglePin(emptyMemory(), "a");
  assert.deepEqual(m.pinned, ["a"]);
  m = togglePin(m, "a");
  assert.deepEqual(m.pinned, []);
  for (let i = 0; i < maxPinned + 2; i++) m = togglePin(m, `p${i}`);
  assert.equal(m.pinned.length, maxPinned);
  assert.equal(m.pinned[0], "p2");
});

test("recentIds is newest first, without pinned items", () => {
  let m = emptyMemory();
  m = recordUse(m, "a", 1);
  m = recordUse(m, "b", 3);
  m = recordUse(m, "c", 2);
  m = togglePin(m, "b");
  assert.deepEqual(recentIds(m, 5), ["c", "a"]);
  assert.deepEqual(recentIds(m, 1), ["c"]);
});
