import assert from "node:assert/strict";
import { test } from "node:test";

import {
  countdown,
  demoEggs,
  demoHides,
  demoPage,
  demoSigned,
  demoState,
  demoUploadLimit,
  isGuest,
  liveSandbox,
  queueText,
  refuseSigning,
  sandboxPollMs,
  sandboxStatus,
  setDemoGuest,
  showBanner,
} from "./demo.ts";

test("the demo page's states", () => {
  const base = { open: true, signedIn: "none" as const, starting: false, error: "" };
  assert.equal(demoState({ ...base, open: undefined }).kind, "checking");
  assert.equal(demoState({ ...base, open: false }).kind, "closed");
  assert.equal(demoState(base).kind, "ready");
  assert.equal(demoState({ ...base, starting: true }).kind, "starting");
  assert.deepEqual(demoState({ ...base, error: "Too many attempts" }), {
    kind: "failed",
    message: "Too many attempts",
  });
  // A real account isn't swapped for a guest, open or not.
  assert.equal(demoState({ ...base, open: false, signedIn: "account" }).kind, "signed-in");
  // A guest can start again (a new hour).
  assert.equal(demoState({ ...base, signedIn: "guest" }).kind, "ready");
});

test("who sees the banner", () => {
  assert.equal(showBanner(true, undefined), true);
  assert.equal(showBanner(false, true), true);
  assert.equal(showBanner(false, false), false);
  assert.equal(showBanner(false, undefined), false);
  assert.equal(isGuest({ guestUntil: {} }), true);
  assert.equal(isGuest({}), false);
  assert.equal(isGuest(undefined), false);
});

test("guests never get a passkey prompt", () => {
  setDemoGuest(false);
  assert.doesNotThrow(refuseSigning);
  setDemoGuest(true);
  assert.throws(refuseSigning, { message: demoSigned });
  setDemoGuest(false);
});

test("what the demo hides, as the Panel refuses it", () => {
  for (const a of [
    "server.create",
    "server.delete",
    "backup.restore",
    "node.update",
    "files.pull",
    "files.import.status",
    "sftp.disconnect",
    "backup.destination.save",
    "backup.key.show",
    "db.password",
    "support.grant",
  ]) {
    assert.equal(demoHides(a), true, a);
  }
  for (const a of [
    "server.start",
    "files.write",
    "addons.install",
    "schedule.run",
    "backup.create",
  ]) {
    assert.equal(demoHides(a), false, a);
  }
  assert.equal(demoHides("backup.create", { locked: true }), true);
  assert.equal(demoHides("server.update", { limits: { memory_mib: 1024 } }), false);
  assert.equal(demoHides("server.update", { egg: "abc" }), true);
  assert.equal(demoHides("server.update", { reinstall: { wipe: false } }), true);
  assert.equal(demoHides("server.update", { egg: "" }), false);
  assert.equal(demoUploadLimit, 10 * 1024 * 1024);
});

test("sandbox polling and countdown", () => {
  assert.equal(sandboxPollMs("queued"), 5_000);
  assert.equal(sandboxPollMs("installing"), 5_000);
  assert.equal(sandboxPollMs("ready"), 30_000);
  assert.equal(sandboxPollMs("gone"), false);
  assert.equal(sandboxPollMs(undefined), false);
  assert.equal(liveSandbox("ready"), true);
  assert.equal(liveSandbox("failed"), false);
  assert.equal(countdown(754_000, 0), "12:34");
  assert.equal(countdown(5_000, 0), "0:05");
  assert.equal(countdown(0, 10_000), "0:00");
  assert.equal(countdown(3_661_000, 0), "1:01:01");
});

test("the queue's words", () => {
  assert.equal(queueText(0, 10), "Next in the queue, starting soon");
  assert.equal(queueText(2, 600), "#3 in the queue, about 10 min");
  const s = { eggName: "Paper", queuePosition: 0, waitSeconds: 0, error: "" };
  assert.equal(sandboxStatus({ ...s, state: "installing" }), "Installing Paper…");
  assert.equal(
    sandboxStatus({ ...s, state: "failed", error: "full" }),
    "Paper couldn't be made: full",
  );
  assert.equal(sandboxStatus({ ...s, state: "gone" }), "Paper was deleted");
});

test("the demo's eggs: fitting first, then popular, then by name", () => {
  const cat = [
    { id: "a", name: "Ark", pop: 50 },
    { id: "b", name: "Bedrock", pop: 1 },
    { id: "c", name: "Cuberite", pop: 1 },
    { id: "p", name: "Paper", pop: 100 },
    { id: "x", name: "Not in the demo", pop: 999 },
  ];
  const rows = demoEggs(
    cat,
    [
      { eggId: "a", fits: false, why: "too big for the demo" },
      { eggId: "c", fits: true, why: "" },
      { eggId: "b", fits: true, why: "" },
      { eggId: "p", fits: true, why: "" },
      { eggId: "gone", fits: true, why: "" },
    ],
    (e) => e.pop,
  );
  assert.deepEqual(
    rows.map((r) => r.egg.id),
    ["p", "b", "c", "a"],
  );
});

test("the pages the demo counts carry no IDs", () => {
  assert.equal(
    demoPage("/orgs/$orgId/servers/$serverId/console"),
    "/orgs/$orgId/servers/$serverId/console",
  );
  assert.equal(demoPage("/orgs/$orgId/"), "/orgs/$orgId");
  assert.equal(demoPage("/"), "/");
  assert.equal(demoPage("/orgs/0193abc"), "");
});
