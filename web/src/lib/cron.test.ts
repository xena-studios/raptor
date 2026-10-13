import assert from "node:assert/strict";
import { test } from "node:test";

import { describeCron } from "./cron.ts";

test("describeCron says the common shapes", () => {
  const cases: [string, string | undefined][] = [
    ["@hourly", "Every hour"],
    ["@daily", "Every day at 12:00 AM"],
    ["0 * * * *", "Every hour"],
    ["15 * * * *", "Every hour at 15 past"],
    ["*/5 * * * *", "Every 5 minutes"],
    ["0 */6 * * *", "Every 6 hours"],
    ["30 */2 * * *", "Every 2 hours, at 30 past"],
    ["0 4 * * *", "Every day at 4:00 AM"],
    ["30 13 * * *", "Every day at 1:30 PM"],
    ["0 0 * * 0", "Every Sunday at 12:00 AM"],
    ["0 9 * * 1-5", "Weekdays at 9:00 AM"],
    ["0 3 1 * *", "On the 1st of every month at 3:00 AM"],
    ["0 3 22 * *", "On the 22nd of every month at 3:00 AM"],
    ["0 3 * 6 *", undefined],
    ["5,35 * * * *", undefined],
    ["nonsense", undefined],
  ];
  for (const [cron, want] of cases) assert.equal(describeCron(cron), want, cron);
});
