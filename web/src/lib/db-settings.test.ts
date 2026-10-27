import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { advancedNames, checkAdvanced, hasAdvanced } from "./db-settings.ts";

// The cases Wings' own test runs (internal/wings/databases/settings_test.go),
// so the app and Wings agree on every one.
type Case = {
  engine: string;
  memory_mib: number;
  text: string;
  want?: [string, string][];
  line?: number;
};
const cases = JSON.parse(
  readFileSync(
    new URL("../../../internal/wings/databases/testdata/advanced_cases.json", import.meta.url),
    "utf8",
  ),
) as Case[];

test("advanced settings are checked as Wings checks them", () => {
  assert.ok(cases.length > 50);
  for (const c of cases) {
    const got = checkAdvanced(c.engine, c.text, c.memory_mib);
    const what = `${c.engine} ${JSON.stringify(c.text)}`;
    if (c.want) {
      assert.equal(got.problem, undefined, what);
      assert.deepEqual(got.settings, c.want, what);
    } else {
      assert.ok(got.problem, `${what}: no problem found`);
      assert.equal(got.problem.line, c.line ?? 0, `${what}: ${got.problem.message}`);
    }
  }
});

test("MongoDB has none", () => {
  assert.equal(hasAdvanced("mongodb"), false);
  assert.equal(hasAdvanced("postgres"), true);
  assert.ok(advancedNames("redis").includes("maxmemory-policy"));
  assert.ok(!advancedNames("redis").includes("maxclients"));
});
