import assert from "node:assert/strict";
import { test } from "node:test";

import { versionChange } from "./db-versions.ts";

test("version changes go in place, by migration, or not down", () => {
  assert.equal(versionChange("mariadb", "10.6", "11.8"), "in-place");
  assert.equal(versionChange("mariadb", "11.4", "11.4"), "in-place");
  assert.equal(versionChange("mariadb", "10.11", "10.6"), "older");
  assert.equal(versionChange("mysql", "8.0", "8.4"), "in-place");
  assert.equal(versionChange("mysql", "8.4", "8.0"), "older");
  assert.equal(versionChange("postgres", "16", "17"), "migrate");
  assert.equal(versionChange("postgres", "17", "17"), "in-place");
  assert.equal(versionChange("postgres", "18", "14"), "older");
  assert.equal(versionChange("mongodb", "7.0", "8.0"), "in-place");
  assert.equal(versionChange("mongodb", "7.0", "9.0"), "skip");
  assert.equal(versionChange("redis", "8.2", "8.10"), "in-place");
});
