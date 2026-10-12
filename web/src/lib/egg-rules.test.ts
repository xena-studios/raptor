import assert from "node:assert/strict";
import { test } from "node:test";

import { readRules, writeRules } from "./egg-rules.ts";

test("rules the kinds write are read back as the same kind", () => {
  for (const [rules, kind] of [
    [["required", "string", "max:20"], "text"],
    [["nullable", "string"], "text"],
    [["required", "integer", "min:1", "max:100"], "number"],
    [["required", "boolean"], "toggle"],
    [["required", "in:peaceful,easy,normal,hard"], "choice"],
  ] as const) {
    const f = readRules([...rules]);
    assert.equal(f.kind, kind, rules.join("|"));
    assert.deepEqual(writeRules(f), rules);
  }
});

test("anything else stays custom, so its rules aren't rewritten", () => {
  for (const rules of [
    ["required", "regex:/^[a-z]+$/"],
    ["required", "string", "between:3,15"],
    ["required", "numeric", "digits_between:1,5"],
    ["required", "in:0,1"],
    ["string", "max:20"],
  ]) {
    assert.equal(readRules(rules).kind, "custom", rules.join("|"));
  }
});
