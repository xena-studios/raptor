import assert from "node:assert/strict";
import { test } from "node:test";

import {
  changes,
  diffList,
  exportList,
  type Installed,
  kindOf,
  ListError,
  listFileName,
  parseList,
  pickVersion,
  stringifyList,
} from "./addon-list.ts";

const h512 = (c: string) => c.repeat(128);
const h1 = (c: string) => c.repeat(40);

const lpAddon = {
  source: "modrinth",
  project: "Vebnzrzj",
  version_id: "v1",
  name: "LuckPerms",
  version_number: "5.4",
};
const lp: Installed = { file: "LuckPerms.jar", sha512: h512("a"), addon: lpAddon };
const ess: Installed = {
  file: "EssentialsX.jar",
  sha512: h512("b"),
  addon: { source: "hangar", project: "EssentialsX", version_id: "10", name: "EssentialsX" },
};
const mine: Installed = { file: "Custom.jar", sha512: h512("c") };

test("export names add-ons by source, project, and version, sorted", () => {
  const list = exportList("plugins", ["paper"], "1.21.4", [lp, mine, ess, { ...lp, file: "dup" }]);
  assert.deepEqual(list, {
    version: 1,
    kinds: {
      plugins: {
        loaders: ["paper"],
        game_version: "1.21.4",
        addons: [
          {
            source: "hangar",
            project: "EssentialsX",
            version_id: "10",
            name: "EssentialsX",
            file: "EssentialsX.jar",
            sha512: h512("b"),
          },
          {
            source: "modrinth",
            project: "Vebnzrzj",
            version_id: "v1",
            name: "LuckPerms",
            version_number: "5.4",
            file: "LuckPerms.jar",
            sha512: h512("a"),
          },
        ],
        by_hand: [{ file: "Custom.jar", sha512: h512("c") }],
      },
    },
  });
  // What's exported reads back the same.
  assert.deepEqual(parseList(stringifyList(list)), list);
});

test("bad lists are refused with a reason", () => {
  const bad = (text: string, re: RegExp) =>
    assert.throws(
      () => parseList(text),
      (e: unknown) => e instanceof ListError && re.test(e.message),
    );
  bad("nope", /isn't JSON/);
  bad("[]", /no version/);
  bad('{"version":2,"kinds":{}}', /newer Raptor/);
  bad('{"version":"1","kinds":{}}', /isn't a number/);
  bad('{"version":1,"kinds":{"plugins":{"addons":[{"source":"modrinth"}]}}}', /no project/);
  bad(
    '{"version":1,"kinds":{"plugins":{"addons":[{"source":"modrinth","project":"x"}]}}}',
    /no version_id/,
  );
  bad(
    '{"version":1,"kinds":{"plugins":{"addons":[{"source":"modrinth","project":"x","version_id":"1","sha1":"zz"}]}}}',
    /sha1 hash/,
  );
  bad(
    '{"version":1,"kinds":{"plugins":{"addons":[{"source":"modrinth","project":"x","version_id":"1","file":"../a.jar"}]}}}',
    /isn't a name/,
  );
  const twice = { source: "modrinth", project: "x", version_id: "1" };
  bad(JSON.stringify({ version: 1, kinds: { plugins: { addons: [twice, twice] } } }), /twice/);
  bad('{"version":1,"kinds":{"plugins":{"by_hand":[{"file":"a.jar"}]}}}', /no sha512/);
  // URLs aren't part of the format: unknown fields are dropped.
  const l = parseList(
    '{"version":1,"kinds":{"mods":{"addons":[{"source":"modrinth","project":"x","version_id":"1","url":"https://evil"}]}}}',
  );
  assert.deepEqual(l.kinds.mods?.addons, [{ source: "modrinth", project: "x", version_id: "1" }]);
  assert.deepEqual(l.kinds.mods?.by_hand, []);
});

test("a list for another kind says which it has", () => {
  const l = parseList('{"version":1,"kinds":{"mods":{}}}');
  assert.throws(() => kindOf(l, "plugins", "plugins"), /no plugins; it has mods/);
  assert.equal(kindOf(l, "mods").addons.length, 0);
});

test("the diff: install, update, remove, same, copy, kept", () => {
  const want = kindOf(
    exportList("plugins", ["paper"], "", [
      { ...lp, addon: { ...lpAddon, version_id: "v2", version_number: "5.5" } },
      {
        file: "ViaVersion.jar",
        addon: { source: "modrinth", project: "P1OZGk5p", version_id: "x" },
      },
      { file: "Shared.jar", sha512: h512("d") },
      { file: "Other.jar", sha512: h512("e") },
    ]),
    "plugins",
  );
  const geyser: Installed = {
    file: "Geyser.jar",
    addon: { source: "modrinth", project: "wKkoqHrH", version_id: "g" },
  };
  const d = diffList(want, [
    lp,
    ess,
    geyser,
    { ...ess, file: "EssentialsX-2.jar" },
    mine,
    { file: "Shared.jar", sha512: h512("d") },
  ]);
  assert.deepEqual(
    d.install.map((a) => a.project),
    ["P1OZGk5p"],
  );
  assert.deepEqual(
    d.update.map((u) => [u.have.file, u.want.version_id]),
    [["LuckPerms.jar", "v2"]],
  );
  assert.deepEqual(d.remove.map((f) => f.file).sort(), [
    "EssentialsX-2.jar",
    "EssentialsX.jar",
    "Geyser.jar",
  ]);
  assert.deepEqual(d.same, []);
  assert.deepEqual(
    d.copy.map((f) => f.file),
    ["Other.jar"],
  );
  assert.deepEqual(
    d.kept.map((f) => f.file),
    ["Custom.jar"],
  );
  assert.equal(changes(d), 5);
  assert.equal(changes(d, false), 2);
  // Applying the same list to the server it came from changes nothing.
  const self = diffList(kindOf(exportList("plugins", [], "", [lp, ess, mine]), "plugins"), [
    lp,
    ess,
    mine,
  ]);
  assert.equal(changes(self), 0);
  assert.equal(self.same.length, 2);
  assert.equal(self.kept.length, 0);
});

test("picking the listed version, checked by hash", () => {
  const file = (sha1: string, url = "https://cdn/x.jar") => ({
    url,
    filename: "x.jar",
    sha512: "",
    sha256: "",
    sha1,
    manualUrl: "",
  });
  const versions = [
    { id: "2", versionNumber: "2.0", file: file(h1("2")) },
    { id: "1", versionNumber: "1.0", file: file(h1("1")) },
  ];
  const want = { source: "umod", project: "x", version_id: "1", sha1: h1("1") };
  const p = pickVersion(want, versions, false);
  assert.ok(p.ok && p.versionId === "1" && !p.newest);
  // Changed upstream.
  assert.deepEqual(pickVersion({ ...want, sha1: h1("f") }, versions, false), {
    ok: false,
    problem: "its file changed at the source since the list was made",
  });
  // Not listed for this server: refused, or the newest with newest.
  const gone = { ...want, version_id: "0", version_number: "0.9" };
  assert.match((pickVersion(gone, versions, false) as { problem: string }).problem, /0.9 isn't/);
  const n = pickVersion(gone, versions, true);
  assert.ok(n.ok && n.versionId === "2" && n.newest);
  assert.match((pickVersion(want, [], true) as { problem: string }).problem, /no version runs/);
  // CurseForge files their authors keep to CurseForge.
  const manual = [
    { id: "1", versionNumber: "1.0", file: { ...file(h1("1"), ""), manualUrl: "https://cf" } },
  ];
  assert.match((pickVersion(want, manual, false) as { problem: string }).problem, /CurseForge/);
});

test("file names", () => {
  assert.equal(listFileName("My Lobby!", "plugins"), "my-lobby-plugins.raptor-addons.json");
  assert.equal(listFileName("", "mods"), "server-mods.raptor-addons.json");
});
