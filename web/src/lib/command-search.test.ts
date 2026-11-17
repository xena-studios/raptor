import assert from "node:assert/strict";
import { test } from "node:test";

import {
  distance,
  matchScore,
  normalize,
  rank,
  recencyBoost,
  wordScore,
} from "./command-search.ts";

const day = 24 * 60 * 60 * 1000;

test("normalize lowercases and strips accents", () => {
  assert.equal(normalize("Café Ünïcode"), "cafe unicode");
});

test("a word matches best whole, then as a prefix, then a word's start", () => {
  const whole = wordScore("survival", "Survival");
  const start = wordScore("surv", "Survival");
  const inner = wordScore("paper", "Minecraft Paper");
  assert.ok(whole > start && start > inner && inner > 0);
});

test("initials match a run of words", () => {
  assert.ok(wordScore("mp", "Minecraft Paper") >= 60);
  assert.ok(wordScore("mjs", "Minecraft: Java Server") >= 60);
});

test("letters in order match, favouring word starts", () => {
  const mcp = wordScore("mcp", "Minecraft Paper");
  assert.ok(mcp > 0, "mcp finds Minecraft Paper");
  // Same letters, none at a word's start.
  assert.ok(mcp > wordScore("mcp", "smacking plates"));
  assert.equal(wordScore("xyz", "Minecraft Paper"), 0);
});

test("typos are forgiven in longer words, not short ones", () => {
  assert.ok(wordScore("survivl", "Survival") > 0, "a missing letter");
  assert.ok(wordScore("suvrival", "Survival") > 0, "two letters swapped");
  assert.ok(wordScore("restrat", "Restart Survival") > 0, "swap in a word being typed");
  assert.equal(wordScore("cat", "Car"), 0, "short words need to match");
  // A typo ranks under a real match.
  assert.ok(wordScore("survivl", "Survival") < wordScore("surv", "Survival"));
});

test("distance counts a swap as one edit", () => {
  assert.equal(distance("abcd", "abdc"), 1);
  assert.equal(distance("kitten", "sitting"), 3);
  assert.equal(distance("", "abc"), 3);
});

test("every query word must match the label or a keyword", () => {
  const item = { label: "Restart Survival", keywords: ["Paper", "node-1"] };
  assert.ok(matchScore("restart surv", item) > 0);
  assert.ok(matchScore("paper restart", item) > 0);
  assert.equal(matchScore("restart creative", item), 0);
  assert.equal(matchScore("", item), 0);
});

test("a keyword match counts less than a label match", () => {
  const byLabel = matchScore("paper", { label: "Paper" });
  const byKeyword = matchScore("paper", { label: "Survival", keywords: ["Paper"] });
  assert.ok(byLabel > byKeyword && byKeyword > 0);
});

test("recency boosts what was used often and lately", () => {
  const now = 100 * day;
  assert.equal(recencyBoost(undefined, now), 0);
  const fresh = recencyBoost({ count: 5, last: now }, now);
  const old = recencyBoost({ count: 5, last: now - 21 * day }, now);
  const once = recencyBoost({ count: 1, last: now }, now);
  assert.ok(fresh > old && fresh > once && old > 0);
  assert.ok(fresh <= 25);
});

test("rank orders by score, boosts, and use, dropping misses", () => {
  const items = [
    { id: "a", label: "Survival" },
    { id: "b", label: "Creative" },
    { id: "c", label: "Survival Games" },
    { id: "d", label: "Skyblock", keywords: ["survival"] },
  ];
  const ids = (r: { item: { id: string } }[]) => r.map((x) => x.item.id);
  assert.deepEqual(ids(rank(items, "survival")), ["a", "c", "d"]);
  // Using "Survival Games" a lot lifts it over "Survival".
  const now = Date.now();
  const uses = new Map([["c", { count: 20, last: now }]]);
  assert.deepEqual(ids(rank(items, "survival", uses, now)), ["c", "a", "d"]);
  // An empty query keeps everything, boosted items first, ties in order.
  const boosted = items.map((x) => (x.id === "b" ? { ...x, boost: 10 } : x));
  assert.deepEqual(ids(rank(boosted, " ")), ["b", "a", "c", "d"]);
});

test("rank is fast enough for thousands of items", () => {
  const items = Array.from({ length: 3000 }, (_, i) => ({
    id: String(i),
    label: `Restart Server number ${i}`,
    keywords: ["Minecraft: Paper", `node-${i % 7}.example.com`],
  }));
  const t = performance.now();
  rank(items, "rstrt srv 42");
  assert.ok(performance.now() - t < 500);
});
