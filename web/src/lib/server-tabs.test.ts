import assert from "node:assert/strict";
import { test } from "node:test";

import { activeServerTab, type ServerTabInputs, serverTabs } from "./server-tabs.ts";

const all = (): ServerTabInputs => ({
  can: () => true,
  game: true,
  addons: true,
  players: true,
  worlds: true,
});

const keys = (t: { key: string }[]) => t.map((x) => x.key);

test("an owner of a server with everything", () => {
  const { bar, more } = serverTabs(all());
  assert.deepEqual(keys(bar), [
    "overview",
    "game",
    "addons",
    "players",
    "worlds",
    "files",
    "backups",
    "schedules",
  ]);
  assert.deepEqual(keys(more), [
    "databases",
    "network",
    "startup",
    "settings",
    "activity",
    "access",
  ]);
  assert.ok(bar.every((t) => t.group === "bar"));
  assert.ok(more.every((t) => t.group === "more"));
});

test("game tabs show only when the egg declares them", () => {
  const { bar } = serverTabs({ ...all(), game: false, players: false, worlds: false });
  assert.deepEqual(keys(bar), ["overview", "addons", "files", "backups", "schedules"]);
});

test("permissions drop tabs", () => {
  const perms = new Set(["files.read", "startup"]);
  const { bar, more } = serverTabs({
    ...all(),
    can: (p) => perms.has(p),
    game: false,
    addons: false,
    players: false,
    worlds: false,
  });
  assert.deepEqual(keys(bar), ["overview", "files"]);
  assert.deepEqual(keys(more), ["network", "startup", "settings"]);
});

test("a lone More tab moves into the bar", () => {
  const { bar, more } = serverTabs({
    can: () => false,
    game: false,
    addons: false,
    players: false,
    worlds: false,
  });
  assert.deepEqual(keys(bar), ["overview", "network"]);
  assert.equal(bar[1]?.group, "bar");
  assert.deepEqual(more, []);
});

test("hidden drops tabs by key", () => {
  const { bar, more } = serverTabs({ ...all(), hidden: new Set(["access", "files"]) });
  assert.ok(!keys(bar).includes("files"));
  assert.ok(!keys(more).includes("access"));
});

test("activeServerTab reads the tab from a URL", () => {
  assert.equal(activeServerTab("/orgs/o/servers/abc"), "overview");
  assert.equal(activeServerTab("/orgs/o/servers/abc/"), "overview");
  assert.equal(activeServerTab("/orgs/o/servers/abc/startup"), "startup");
  assert.equal(activeServerTab("/orgs/o/servers/abc/databases/db1"), "databases");
  assert.equal(activeServerTab("/orgs/o/servers/abc/addons/plugins"), "addons");
  assert.equal(activeServerTab("/orgs/o/servers/abc/workshop"), "addons");
  assert.equal(activeServerTab("/orgs/o/servers/abc/nope"), undefined);
  assert.equal(activeServerTab("/orgs/o/servers"), undefined);
});
