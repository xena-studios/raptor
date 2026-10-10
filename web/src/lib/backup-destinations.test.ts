// Run with `pnpm test` (node --test).
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  describeRetention,
  formatSpeed,
  needsPasskey,
  type Policy,
  presetEndpoint,
  presetFor,
  presetOf,
  regionOf,
  s3Presets,
} from "./backup-destinations.ts";

const target = (id: string, last: number, daily = 0) => ({
  destination_id: id,
  keep_last: last,
  keep_daily: daily,
  keep_weekly: 0,
  keep_monthly: 0,
});

// The same rule as Wings' signing check (internal/wings/actions):
// TestPolicyJSON in internal/wings/backup has the Go side.
test("a passkey for new places and for keeping less", () => {
  const cur: Policy = { targets: [target("local", 3, 7)] };
  assert.equal(needsPasskey({ targets: [target("local", 3, 7)] }, cur), false);
  assert.equal(needsPasskey({ targets: [target("local", 5, 7)] }, cur), false, "keeping more");
  assert.equal(needsPasskey({ targets: [target("local", 3, 2)] }, cur), true, "keeping fewer");
  assert.equal(
    needsPasskey({ targets: [target("local", 3, 7), target("b2", 1)] }, cur),
    true,
    "a new destination",
  );
  assert.equal(
    needsPasskey(
      { targets: [target("b2", 1)] },
      { targets: [target("local", 3), target("b2", 1)] },
    ),
    false,
    "dropping one",
  );
  assert.equal(
    needsPasskey({ targets: [target("local", 3)] }, { targets: [target("b2", 3)] }),
    false,
    "the node's own disk",
  );
});

test("S3 presets fill and read back the endpoint", () => {
  const b2 = s3Presets.find((p) => p.id === "b2");
  assert.ok(b2);
  assert.equal(presetEndpoint(b2, "eu-central-003"), "s3.eu-central-003.backblazeb2.com");
  assert.equal(presetFor("s3.eu-central-003.backblazeb2.com").id, "b2");
  assert.equal(regionOf(b2, "s3.eu-central-003.backblazeb2.com"), "eu-central-003");
  assert.equal(presetFor("https://storage.googleapis.com").id, "gcs");
  assert.equal(presetFor("minio.lan:9000").id, "custom");
});

test("retention in words, and its preset", () => {
  assert.equal(describeRetention(target("x", 3, 7)), "Keeps the last 3, 7 daily");
  assert.equal(
    presetOf({ keep_last: 3, keep_daily: 7, keep_weekly: 4, keep_monthly: 0 }),
    "standard",
  );
  assert.equal(
    presetOf({ keep_last: 2, keep_daily: 0, keep_weekly: 0, keep_monthly: 0 }),
    "custom",
  );
});

test("upload speed", () => {
  assert.equal(formatSpeed(0), "No limit");
  assert.equal(formatSpeed(5_000_000), "5 MB/s");
  assert.equal(formatSpeed(2_500_000), "2.5 MB/s");
  assert.equal(formatSpeed(25_000_000), "25 MB/s");
});
