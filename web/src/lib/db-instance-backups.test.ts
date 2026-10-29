import assert from "node:assert/strict";
import { test } from "node:test";

import {
  type InstanceBackup,
  type InstanceBackupSettings,
  inProgress,
  settingsNeedPasskey,
} from "./db-instance-backups.ts";

const cur: InstanceBackupSettings = {
  enabled: true,
  at: "04:00",
  keep_last: 7,
  destination_id: "local",
};

test("settingsNeedPasskey: keeping fewer, or somewhere new off the node", () => {
  assert.equal(settingsNeedPasskey({ ...cur, at: "05:30", enabled: false }, cur), false);
  assert.equal(settingsNeedPasskey({ ...cur, keep_last: 9 }, cur), false);
  assert.equal(settingsNeedPasskey({ ...cur, keep_last: 3 }, cur), true);
  assert.equal(settingsNeedPasskey({ ...cur, destination_id: "b2" }, cur), true);
  assert.equal(settingsNeedPasskey({ ...cur, destination_id: "raptor" }, cur), false);
  // Fewer on another destination keeps everything already on the old one.
  assert.equal(
    settingsNeedPasskey(
      { ...cur, destination_id: "local", keep_last: 1 },
      {
        ...cur,
        destination_id: "b2",
      },
    ),
    false,
  );
});

test("inProgress", () => {
  const b = (status: InstanceBackup["status"]): InstanceBackup => ({
    id: status,
    destination_id: "local",
    kind: "manual",
    status,
    size: 0,
    files: 0,
    created_at: "2026-10-29T04:00:00Z",
  });
  assert.equal(inProgress([b("ok"), b("failed")]), false);
  assert.equal(inProgress([b("ok"), b("running")]), true);
  assert.equal(inProgress([b("pending")]), true);
});
