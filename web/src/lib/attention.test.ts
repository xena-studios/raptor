import assert from "node:assert/strict";
import { test } from "node:test";

import {
  type Addon,
  addonItems,
  diskItem,
  eggItem,
  installItem,
  rank,
  scanItem,
  sortItems,
  whoCan,
} from "./attention.ts";

test("a failed install, with its error and who can reinstall", () => {
  assert.equal(installItem({ installState: "installed", installError: "" }, true), undefined);
  const it = installItem({ installState: "failed", installError: "exit code 1." }, true);
  assert.equal(it?.rank, rank.broken);
  assert.equal(
    it?.text,
    "The install failed, so the server can't start: exit code 1. Fix the cause, then reinstall it.",
  );
  assert.deepEqual(it?.action, { label: "Reinstall", to: "settings" });
  const ro = installItem({ installState: "", installError: "", state: "install_failed" }, false);
  assert.equal(ro?.action, undefined);
  assert.equal(
    ro?.blocked,
    'Someone with the "Reinstall" permission can reinstall it from Settings.',
  );
  assert.equal(ro?.sig, "failed");
});

test("low disk shows from 90%, and its signature steps", () => {
  assert.equal(diskItem(50, 100, true), undefined);
  assert.equal(diskItem(95, 0, true), undefined); // no limit
  const at90 = diskItem(90, 100, true);
  assert.equal(at90?.sig, "90");
  assert.equal(at90?.rank, rank.risk);
  assert.deepEqual(at90?.action, { label: "Open Files", to: "files" });
  assert.equal(diskItem(94, 100, true)?.sig, "90");
  assert.equal(diskItem(96, 100, true)?.sig, "95");
  const full = diskItem(120, 100, false);
  assert.equal(full?.sig, "99");
  assert.equal(full?.rank, rank.broken);
  assert.match(full?.text ?? "", /100% full/);
  assert.equal(full?.action, undefined);
  assert.match(full?.blocked ?? "", /"Change files"/);
});

const a = (over: Partial<Addon>): Addon => ({
  sha512: "h",
  title: "A",
  projectId: "p",
  required: [],
  ...over,
});

test("add-ons missing a dependency, unless a hand-added file may be it", () => {
  const addons = [
    a({ sha512: "1", title: "Sodium Extra", projectId: "x", required: ["sodium"] }),
    a({ sha512: "2", title: "Lithium", projectId: "l", required: ["x"] }),
  ];
  const [missing] = addonItems("mods", "mod", addons, false, true);
  assert.equal(missing?.id, "missing:mods");
  assert.equal(missing?.text, "Sodium Extra is missing another mod it needs, so it may not load.");
  assert.deepEqual(missing?.action, {
    label: "Install what's missing",
    to: "addons",
    kind: "mods",
  });
  assert.deepEqual(addonItems("mods", "mod", addons, true, true), []);
  const two = addonItems(
    "mods",
    "mod",
    [a({ sha512: "1", required: ["q"] }), a({ sha512: "2", projectId: "r", required: ["q"] })],
    false,
    false,
  );
  assert.equal(two[0]?.text, "2 mods are missing others they need, so they may not load.");
  assert.equal(two[0]?.blocked, 'Someone with the "Change files" permission can install them.');
});

test("add-on updates, named when there's one", () => {
  assert.deepEqual(addonItems("plugins", "plugin", [a({})], false, true), []);
  const [one] = addonItems(
    "plugins",
    "plugin",
    [a({ title: "LuckPerms", latestId: "v2" })],
    false,
    true,
  );
  assert.equal(one?.text, "LuckPerms has an update.");
  assert.equal(one?.sig, "v2");
  assert.equal(one?.rank, rank.update);
  const [many] = addonItems(
    "plugins",
    "plugin",
    [a({ latestId: "v2" }), a({ projectId: "q", latestId: "v3" })],
    false,
    true,
  );
  assert.equal(many?.text, "2 plugins have an update.");
  assert.equal(many?.sig, "v2,v3");
});

test("a newer egg", () => {
  assert.equal(eggItem({ name: "Paper", latest: 3, current: 3, outdated: false }), undefined);
  assert.equal(
    eggItem({ name: "Paper", latest: 4, current: 3, outdated: true })?.text,
    "A newer version of Paper (version 4) is available; it runs version 3.",
  );
  assert.equal(
    eggItem({ name: "Paper", latest: 4, outdated: true })?.text,
    "A newer version of Paper (version 4) is available.",
  );
});

test("worst first, ties in order", () => {
  const egg = eggItem({ name: "Paper", latest: 4, outdated: true });
  const disk = diskItem(91, 100, true);
  const install = installItem({ installState: "failed", installError: "" }, true);
  const [u] = addonItems("mods", "mod", [a({ latestId: "v" })], false, true);
  const sorted = sortItems([egg, disk, u, install].filter((x) => !!x));
  assert.deepEqual(
    sorted.map((i) => i.id),
    ["install", "disk", "egg", "updates:mods"],
  );
});

test("who can", () => {
  assert.equal(whoCan("*", "do it"), "An org admin or owner can do it.");
  assert.equal(whoCan("backups", "do it"), 'Someone with the "backups" permission can do it.');
});

test("the resource scan's row", () => {
  assert.equal(scanItem({ high: 0, medium: 0 }, 0), undefined);
  const hi = scanItem({ high: 2, medium: 5 }, 1);
  assert.equal(hi?.rank, rank.security);
  assert.match(hi?.text ?? "", /2 high-severity lines in 1 file/);
  assert.equal(hi?.action?.to, "scan");
  assert.match(scanItem({ high: 0, medium: 1 }, 1)?.text ?? "", /1 medium-severity line in 1 file/);
});
