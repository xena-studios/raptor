// Run with `pnpm test` (node --test).
import assert from "node:assert/strict";
import { test } from "node:test";

import { duration, skipText, stepFailure } from "./schedule-runs.ts";

test("known step errors become sentences", () => {
  assert.equal(
    stepFailure({ type: "command", ok: false, error: "server isn't running" }),
    "the server wasn't running",
  );
  assert.equal(
    stepFailure({ type: "power", ok: false, error: "server is over its disk limit; free space" }),
    "the server was over its disk limit",
  );
});

test("unknown step errors don't show the node's words", () => {
  const got = stepFailure({ type: "backup", ok: false, error: "backup failed: rpc error: EOF" });
  assert.equal(got, "the backup failed");
  assert.equal(stepFailure({ type: "odd", ok: false, error: "x" }), "it failed");
});

test("skip reasons", () => {
  assert.equal(skipText("offline"), "the server was offline");
  assert.equal(skipText("new_reason"), "it couldn't run");
});

test("durations", () => {
  assert.equal(duration(4_200), "4s");
  assert.equal(duration(125_000), "2m 5s");
  assert.equal(duration(120_000), "2m");
  assert.equal(duration(3_780_000), "1h 3m");
});
