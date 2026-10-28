import assert from "node:assert/strict";
import { test } from "node:test";

import { formatQueries, fraction, queriesNote } from "./db-usage.ts";

test("queries a second read as tenths, whole, then thousands", () => {
  assert.equal(formatQueries(0), "0.0/s");
  assert.equal(formatQueries(0.26), "0.3/s");
  assert.equal(formatQueries(9.94), "9.9/s");
  assert.equal(formatQueries(42.4), "42/s");
  assert.equal(formatQueries(999.4), "999/s");
  assert.equal(formatQueries(1250), "1.3k/s");
  assert.equal(formatQueries(48_200), "48k/s");
});

test("each engine says what it counts", () => {
  assert.equal(queriesNote("mariadb"), "Statements a second");
  assert.equal(queriesNote("mysql"), "Statements a second");
  assert.equal(queriesNote("postgres"), "Transactions a second");
  assert.equal(queriesNote("valkey"), "Commands a second");
  assert.equal(queriesNote("mongodb"), "Operations a second");
});

test("no limit, no fraction", () => {
  assert.equal(fraction(256, 1024), 0.25);
  assert.equal(fraction(256, 0), undefined);
});
