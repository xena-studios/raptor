import assert from "node:assert/strict";
import { test } from "node:test";

import { duration, filename, gridSteps, summarize } from "./chart-image.ts";

test("summaries", () => {
  const data = [
    { t: 1, values: [2, null] },
    { t: 2, values: [null, null] },
    { t: 3, values: [6, null] },
    { t: 4, values: [4, null] },
  ];
  assert.deepEqual(summarize(data, 0), { last: 4, min: 2, max: 6, avg: 4 });
  assert.deepEqual(summarize(data, 1), {});
});

test("file names", () => {
  const at = new Date(2026, 9, 16, 14, 5);
  assert.equal(
    filename("CPU", "smp on Basement Box", at),
    "raptor-smp-on-basement-box-cpu-2026-10-16-1405.png",
  );
  assert.equal(filename("Nodes", undefined, at), "raptor-nodes-2026-10-16-1405.png");
});

test("durations", () => {
  assert.equal(duration(3600_000), "1 hour");
  assert.equal(duration(6 * 3600_000), "6 hours");
  assert.equal(duration(7 * 86400_000), "7 days");
  assert.equal(duration(15 * 60_000), "15 minutes");
});

test("grid steps land on round values", () => {
  // 5 MB/s in fifths (1, 2, 3…), 200% in quarters (50, 100…), 2.5 in fifths.
  assert.equal(gridSteps(5e6), 5);
  assert.equal(gridSteps(200), 4);
  assert.equal(gridSteps(2.5), 5);
  assert.equal(gridSteps(1000), 4);
});
