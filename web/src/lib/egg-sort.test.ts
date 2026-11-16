import assert from "node:assert/strict";
import { test } from "node:test";

import { libraryId, popularity, sortEggs } from "./egg-sort.ts";

const egg = (name: string, o: Record<string, unknown> = {}) => ({
  name,
  scope: "official",
  status: "published",
  certified: false,
  servers: 0,
  copies: 0,
  sourceUrl: "",
  updatedAt: undefined as { seconds: bigint } | undefined,
  ...o,
});
const lib = (id: string) =>
  `https://github.com/xena-studios/raptor/blob/main/library/${id}/egg-x.json`;

test("library IDs and popularity", () => {
  assert.equal(libraryId({ sourceUrl: lib("minecraft/paper") }), "minecraft/paper");
  assert.equal(libraryId({ sourceUrl: "https://example.com/egg.json" }), "");
  // The head start orders a new Panel; real use outweighs it.
  assert.ok(
    popularity({ servers: 0, sourceUrl: lib("minecraft/paper") }) >
      popularity({ servers: 0, sourceUrl: lib("steam/rust") }),
  );
  assert.ok(
    popularity({ servers: 3, sourceUrl: lib("steam/wreckfest") }) >
      popularity({ servers: 0, sourceUrl: lib("minecraft/paper") }),
  );
});

test("sorting the library", () => {
  const list = [
    egg("Abiotic Factor", {
      sourceUrl: lib("steam/abiotic-factor"),
      servers: 1,
      updatedAt: { seconds: 300n },
    }),
    egg("Paper", {
      sourceUrl: lib("minecraft/paper"),
      certified: true,
      updatedAt: { seconds: 100n },
      copies: 2,
    }),
    egg("Rust", { sourceUrl: lib("steam/rust"), updatedAt: { seconds: 200n } }),
    egg("Draft one", { status: "draft", servers: 50 }),
    egg("Ours", { scope: "org" }),
  ];
  const names = (by: Parameters<typeof sortEggs>[1]) => sortEggs(list, by).map((e) => e.name);
  // The org's own first, drafts last, whatever the order.
  // One server (10) doesn't outrank Paper's or Rust's head start yet.
  assert.deepEqual(names("popular"), ["Ours", "Paper", "Rust", "Abiotic Factor", "Draft one"]);
  assert.deepEqual(names("name"), ["Ours", "Abiotic Factor", "Paper", "Rust", "Draft one"]);
  // Paper ahead of Rust at 0 servers: certified first.
  assert.deepEqual(names("servers"), ["Ours", "Abiotic Factor", "Paper", "Rust", "Draft one"]);
  assert.deepEqual(names("newest"), ["Ours", "Abiotic Factor", "Rust", "Paper", "Draft one"]);
  assert.deepEqual(names("copies"), ["Ours", "Paper", "Abiotic Factor", "Rust", "Draft one"]);
  // With no servers yet, Popular is the head start.
  const fresh = list.map((e) => ({ ...e, servers: 0 }));
  assert.deepEqual(
    sortEggs(fresh, "popular").map((e) => e.name),
    ["Ours", "Paper", "Rust", "Abiotic Factor", "Draft one"],
  );
});
