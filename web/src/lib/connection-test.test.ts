// Run with `pnpm test` (node --test).
import assert from "node:assert/strict";
import { test } from "node:test";

import { diagnose, type Inside, type Outside } from "./connection-test.ts";

const out = (port: number, reachable: boolean, failure = ""): Outside => ({
  port,
  primary: port === 25565,
  reachable,
  failure,
});
const running = (listening: Inside["listening"]): Inside => ({
  state: "running",
  checked: true,
  listening,
});

test("reachable is fine whatever the inside says", () => {
  assert.equal(diagnose([out(25565, true)], undefined)[0]?.cause, "ok");
});

test("a stopped server needs starting first", () => {
  const v = diagnose([out(25565, false, "refused")], {
    state: "offline",
    checked: false,
    listening: [],
  });
  assert.equal(v[0]?.cause, "not_running");
});

test("listening inside but not reachable outside is a firewall", () => {
  const v = diagnose([out(25565, false, "timeout")], running([{ port: 25565, proto: "tcp" }]));
  assert.equal(v[0]?.cause, "blocked");
});

test("a game on another port says which", () => {
  const v = diagnose([out(25565, false, "refused")], running([{ port: 25570, proto: "tcp" }]));
  assert.equal(v[0]?.cause, "wrong_port");
  assert.deepEqual(v[0]?.others, [25570]);
});

test("a game on localhost only", () => {
  const v = diagnose(
    [out(25565, false, "refused")],
    running([{ port: 25565, proto: "tcp", loopback: true }]),
  );
  assert.equal(v[0]?.cause, "loopback");
});

test("UDP games can't be judged over TCP", () => {
  const v = diagnose([out(19132, false, "refused")], running([{ port: 19132, proto: "udp" }]));
  assert.equal(v[0]?.cause, "ok_udp");
});

test("nothing listening yet", () => {
  assert.equal(diagnose([out(25565, false, "refused")], running([]))[0]?.cause, "starting");
});

test("127.0.0.1 allocations aren't public on purpose", () => {
  assert.equal(diagnose([out(25568, false, "local_only")], running([]))[0]?.cause, "local_only");
});

test("without the node's side, a timeout still points at a firewall", () => {
  assert.equal(diagnose([out(25565, false, "timeout")], undefined)[0]?.cause, "blocked");
  assert.equal(diagnose([out(25565, false, "refused")], undefined)[0]?.cause, "unknown");
});

test("a forwarder that hangs up points at the game, not a firewall", () => {
  assert.equal(diagnose([out(25565, false, "closed")], running([]))[0]?.cause, "starting");
  assert.equal(
    diagnose([out(25565, false, "closed")], running([{ port: 25565, proto: "tcp" }]))[0]?.cause,
    "unknown",
  );
});
