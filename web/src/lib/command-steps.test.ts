import assert from "node:assert/strict";
import { test } from "node:test";

import type { ServerAct } from "./command-items.ts";
import {
  back,
  crumbs,
  current,
  isInput,
  type Names,
  onBackspace,
  placeholder,
  push,
  rootStack,
  setQuery,
} from "./command-steps.ts";

const names: Names = {
  server: (id) => ({ s1: "Survival" })[id] ?? id,
  node: (id) => ({ n1: "node-1" })[id] ?? id,
  egg: (id) => id,
  verb: (a: ServerAct) => ({ command: "Send a console command…" })[a as "command"] ?? a,
  short: (a: ServerAct) =>
    ({ command: "Send a command", files: "Open files at…" })[a as "command"] ?? a,
};

test("steps stack up, each keeping its own query", () => {
  let s = setQuery(rootStack, "send");
  s = push(s, { kind: "pick", act: "command" });
  assert.equal(current(s).query, "");
  s = setQuery(s, "surv");
  s = push(s, { kind: "input", act: "command", serverId: "s1" });
  assert.equal(s.length, 3);
  s = back(s);
  assert.deepEqual(current(s), { step: { kind: "pick", act: "command" }, query: "surv" });
  s = back(back(s));
  assert.equal(current(s).query, "send", "the root's query is back");
  assert.equal(back(s), s, "the root stays");
});

test("Backspace goes back only from an empty input below the root", () => {
  const s = push(rootStack, { kind: "server", serverId: "s1" });
  assert.equal(onBackspace(s, "x"), undefined, "edits the text");
  assert.equal(onBackspace(rootStack, ""), undefined, "nothing to go back to");
  assert.deepEqual(onBackspace(s, ""), rootStack);
});

test("crumbs name each step, without repeating the act", () => {
  let s = push(rootStack, { kind: "pick", act: "command" });
  assert.deepEqual(crumbs(s, names), ["Send a console command"]);
  s = push(s, { kind: "input", act: "command", serverId: "s1" });
  assert.deepEqual(crumbs(s, names), ["Send a console command", "Survival"]);
  let t = push(rootStack, { kind: "server", serverId: "s1" });
  t = push(t, { kind: "input", act: "files", serverId: "s1" });
  assert.deepEqual(crumbs(t, names), ["Survival", "Open files at"]);
  assert.deepEqual(crumbs(rootStack, names), []);
});

test("input steps hold a value, others a search", () => {
  assert.ok(isInput({ kind: "input", act: "command", serverId: "s1" }));
  assert.ok(isInput({ kind: "invite" }));
  assert.ok(!isInput({ kind: "pick", act: "command" }));
  assert.equal(placeholder({ kind: "input", act: "command", serverId: "s" }, "Type it"), "Type it");
  assert.match(placeholder({ kind: "root" }), /Search/);
});
