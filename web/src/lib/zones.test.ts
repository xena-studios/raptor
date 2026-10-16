import assert from "node:assert/strict";
import { test } from "node:test";

import { difference, offsetMinutes, wallTime } from "./zones.ts";

test("offsets", () => {
  const winter = new Date("2026-01-15T12:00:00Z");
  const summer = new Date("2026-07-15T12:00:00Z");
  assert.equal(offsetMinutes("UTC", winter), 0);
  assert.equal(offsetMinutes("Europe/Paris", winter), 60);
  assert.equal(offsetMinutes("Europe/Paris", summer), 120);
  assert.equal(offsetMinutes("America/New_York", winter), -300);
  assert.equal(offsetMinutes("Asia/Kolkata", winter), 330);
});

test("wall times", () => {
  const on = new Date("2026-07-15T12:00:00Z");
  assert.equal(wallTime("Europe/Paris", 9, 30, on).toISOString(), "2026-07-15T07:30:00.000Z");
  assert.equal(wallTime("America/New_York", 23, 0, on).toISOString(), "2026-07-16T03:00:00.000Z");
});

test("differences", () => {
  const at = new Date("2026-01-15T12:00:00Z");
  assert.equal(difference("Europe/Paris", "UTC", at), "+1h");
  assert.equal(difference("America/New_York", "Asia/Kolkata", at), "−10h 30m");
  assert.equal(difference("UTC", "Etc/UTC", at), "same time");
});
