import assert from "node:assert/strict";
import { test } from "node:test";

import { sessionCache } from "./session-cache.ts";

function setup() {
  let t = 0;
  let loads = 0;
  const cache = sessionCache<number>(30_000, () => t);
  const load = async () => ++loads;
  return {
    cache,
    load,
    loads: () => loads,
    tick: (ms: number) => {
      t += ms;
    },
  };
}

test("a fresh session is reused", async () => {
  const { cache, load, loads, tick } = setup();
  assert.equal(await cache.get(load), 1);
  tick(29_999);
  assert.equal(await cache.get(load), 1);
  assert.equal(loads(), 1);
  tick(1);
  assert.equal(await cache.get(load), 2);
});

test("callers share a load in flight", async () => {
  const { cache, load, loads } = setup();
  const [a, b] = await Promise.all([cache.get(load), cache.get(load)]);
  assert.equal(a, 1);
  assert.equal(b, 1);
  assert.equal(loads(), 1);
});

test("clear makes the next caller ask again", async () => {
  const { cache, load } = setup();
  assert.equal(await cache.get(load), 1);
  cache.clear();
  assert.equal(await cache.get(load), 2);
});

test("a clear while loading isn't undone by that load", async () => {
  const { cache, load } = setup();
  let finish: (v: number) => void = () => {};
  const slow = cache.get(() => new Promise<number>((r) => (finish = r)));
  cache.clear();
  finish(99);
  assert.equal(await slow, 99);
  assert.equal(await cache.get(load), 1);
});

test("a failed load isn't kept", async () => {
  const { cache, load } = setup();
  await assert.rejects(cache.get(async () => Promise.reject(new Error("down"))));
  assert.equal(await cache.get(load), 1);
});
