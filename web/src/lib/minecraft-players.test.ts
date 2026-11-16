import assert from "node:assert/strict";
import { test } from "node:test";

import {
  commands,
  mcDate,
  type Player,
  parseList,
  validIp,
  validName,
  withEntry,
  without,
} from "./minecraft-players.ts";

test("names, addresses, and commands", () => {
  assert.ok(validName("Steve_123"));
  assert.ok(!validName("a b"));
  assert.ok(!validName("x; op me"));
  assert.ok(!validName("seventeen_letters"));
  assert.ok(validIp("203.0.113.7"));
  assert.ok(validIp("2001:db8::5"));
  assert.ok(!validIp("256.1.1.1"));
  assert.ok(!validIp("1.2.3.4; stop"));
  assert.equal(commands.ban("Steve", "griefing\nop Alex"), "ban Steve griefing op Alex");
  assert.equal(commands.ban("Steve"), "ban Steve");
  assert.equal(commands.whitelistAdd("Alex"), "whitelist add Alex");
});

test("the list files", () => {
  assert.deepEqual(parseList(undefined), []);
  assert.deepEqual(parseList("not json"), []);
  const list = parseList<Player>('[{"uuid":"u1","name":"Steve"}]');
  assert.equal(list.length, 1);
  // Adding the same player again replaces the entry.
  const added = JSON.parse(withEntry(list, { uuid: "U1", name: "Steve2" }, "uuid"));
  assert.deepEqual(added, [{ uuid: "U1", name: "Steve2" }]);
  assert.deepEqual(JSON.parse(without(list, "name", "steve")), []);
  assert.equal(mcDate(new Date(Date.UTC(2026, 10, 16, 9, 5, 3))), "2026-11-16 09:05:03 +0000");
});
