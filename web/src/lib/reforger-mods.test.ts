import assert from "node:assert/strict";
import { test } from "node:test";

import {
  addMods,
  indentOf,
  modRef,
  parseConfig,
  pinMod,
  removeMod,
  sameMods,
  withMods,
} from "./reforger-mods.ts";

// The egg's config.json, as it makes it, with a mod.
const config = `{
    "bindPort": 2001,
    "a2s": {
        "address": "0.0.0.0",
        "port": 17777
    },
    "game": {
        "name": "An Arma Reforger server",
        "admins": [],
        "gameProperties": {
            "battlEye": true
        },
        "mods": [
            {
                "modId": "595F2BF2F44836FB",
                "name": "RHS - Status Quo",
                "version": "0.17.5281",
                "required": true
            }
        ]
    },
    "operating": {
        "lobbyPlayerSynchronise": true
    }
}
`;

test("parseConfig reads game.mods, keeping keys it doesn't know", () => {
  const p = parseConfig(config);
  assert.ok(p.ok);
  assert.deepEqual(p.mods, [
    { modId: "595F2BF2F44836FB", name: "RHS - Status Quo", version: "0.17.5281", required: true },
  ]);
});

test("parseConfig: no game block or no mods is no mods", () => {
  assert.deepEqual(parseConfig("{}"), { ok: true, mods: [] });
  assert.deepEqual(parseConfig(`{"game": {"name": "x"}}`), { ok: true, mods: [] });
});

test("parseConfig: an empty version is the newest", () => {
  const p = parseConfig(`{"game": {"mods": [{"modId": "A", "name": "a", "version": ""}]}}`);
  assert.ok(p.ok);
  assert.deepEqual(p.mods, [{ modId: "A", name: "a" }]);
});

test("parseConfig refuses what saving would break", () => {
  for (const bad of [
    "{",
    "[]",
    `{"game": []}`,
    `{"game": {"mods": {}}}`,
    `{"game": {"mods": [{"name": "no id"}]}}`,
  ]) {
    const p = parseConfig(bad);
    assert.equal(p.ok, false, bad);
  }
});

test("withMods changes only game.mods, in the file's indent and order", () => {
  const p = parseConfig(config);
  assert.ok(p.ok);
  const mods = addMods(p.mods, [{ modId: "1337C0DE5DABBEEF", name: "RHS - Content Pack 01" }]);
  const out = withMods(config, mods);
  const doc = JSON.parse(out);
  assert.deepEqual(Object.keys(doc), ["bindPort", "a2s", "game", "operating"]);
  assert.deepEqual(Object.keys(doc.game), ["name", "admins", "gameProperties", "mods"]);
  assert.equal(doc.game.gameProperties.battlEye, true);
  assert.equal(doc.game.mods.length, 2);
  assert.deepEqual(doc.game.mods[1], { modId: "1337C0DE5DABBEEF", name: "RHS - Content Pack 01" });
  assert.equal(doc.game.mods[0].required, true);
  assert.ok(out.endsWith("}\n"));
  assert.ok(out.includes('\n    "bindPort": 2001'));
  // Saving what's there changes nothing.
  assert.equal(withMods(config, p.mods), config);
});

test("withMods adds a game block and a list when there's none", () => {
  const out = withMods(`{\n  "bindPort": 2001\n}`, [{ modId: "A", name: "a" }]);
  assert.equal(
    out,
    `{\n  "bindPort": 2001,\n  "game": {\n    "mods": [\n      {\n        "modId": "A",\n        "name": "a"\n      }\n    ]\n  }\n}`,
  );
});

test("withMods throws on a config it can't read", () => {
  assert.throws(() => withMods("not json", []));
});

test("indentOf", () => {
  assert.equal(indentOf(config), "    ");
  assert.equal(indentOf(`{\n\t"a": 1\n}`), "\t");
  assert.equal(indentOf("{}"), 4);
});

test("modRef takes a link or an ID", () => {
  assert.equal(modRef("595F2BF2F44836FB"), "595F2BF2F44836FB");
  assert.equal(modRef(" 595f2bf2f44836fb "), "595F2BF2F44836FB");
  assert.equal(
    modRef("https://reforger.armaplatform.com/workshop/595F2BF2F44836FB-RHS-StatusQuo"),
    "595F2BF2F44836FB",
  );
  assert.equal(
    modRef("https://reforger.armaplatform.com/workshop/595F2BF2F44836FB"),
    "595F2BF2F44836FB",
  );
  for (const bad of [
    "",
    "595F2BF2F44836F",
    "595F2BF2F44836FBA",
    "https://example.com/workshop/x",
  ]) {
    assert.equal(modRef(bad), undefined, bad);
  }
});

test("addMods, removeMod, pinMod", () => {
  const a = { modId: "A", name: "a" };
  const b = { modId: "B", name: "b", version: "1.0", required: false };
  assert.deepEqual(addMods([a], [a, b]), [a, b]);
  assert.deepEqual(removeMod([a, b], "A"), [b]);
  assert.deepEqual(pinMod([a, b], "A", "2.0"), [{ ...a, version: "2.0" }, b]);
  assert.deepEqual(pinMod([a, b], "B", undefined), [a, { modId: "B", name: "b", required: false }]);
  assert.ok(sameMods([a], [{ modId: "A", name: "a" }]));
  assert.ok(!sameMods([a], [b]));
});
