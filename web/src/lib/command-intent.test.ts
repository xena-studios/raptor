import assert from "node:assert/strict";
import { test } from "node:test";

import { hasIntent, intentLifetime, leaveIntent, takeIntent } from "./command-intent.ts";

test("an intent is taken once, by its kind and server", () => {
  leaveIntent({ kind: "players", serverId: "s1", op: "whitelist", name: "Steve" }, 0);
  assert.equal(takeIntent("players", "s2", 1), undefined, "another server's page");
  assert.equal(takeIntent("addons.update", "s1", 1), undefined, "another page");
  assert.ok(hasIntent("players", "s1", 1));
  const i = takeIntent("players", "s1", 1);
  assert.deepEqual(i, { kind: "players", serverId: "s1", op: "whitelist", name: "Steve" });
  assert.equal(takeIntent("players", "s1", 2), undefined, "already taken");
  assert.equal(hasIntent("players", "s1", 2), false);
});

test("an org's intent is scoped by the org", () => {
  leaveIntent({ kind: "member.invite", orgId: "o1", email: "a@b.c" }, 0);
  assert.equal(takeIntent("member.invite", "o2", 1), undefined);
  assert.equal(takeIntent("member.invite", "o1", 1)?.email, "a@b.c");
});

test("an intent nobody takes expires", () => {
  leaveIntent({ kind: "connection.test", serverId: "s1" }, 0);
  assert.equal(hasIntent("connection.test", "s1", intentLifetime + 1), false);
  assert.equal(takeIntent("connection.test", "s1", intentLifetime + 1), undefined);
});

test("a new intent replaces the last", () => {
  leaveIntent({ kind: "connection.test", serverId: "s1" }, 0);
  leaveIntent({ kind: "log.share", serverId: "s1" }, 1);
  assert.equal(takeIntent("connection.test", "s1", 2), undefined);
  assert.ok(takeIntent("log.share", "s1", 2));
});
