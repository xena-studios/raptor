import assert from "node:assert/strict";
import { test } from "node:test";

import {
  dstOverrideIds,
  dstSetupIds,
  iniList,
  jsonIds,
  modInfoId,
  setDstOverrides,
  setDstSetup,
  setIniList,
  setJsonIds,
  workshopPath,
  zomboidModId,
} from "./workshop-files.ts";

test("workshopPath fills variables and refuses ones that leave the folder", () => {
  const vars: Record<string, string> = { NAME: "servertest", BAD: "../x", EMPTY: "" };
  const v = (env: string) => vars[env] ?? "";
  assert.equal(workshopPath(".cache/Server/{{NAME}}.ini", v), ".cache/Server/servertest.ini");
  assert.equal(workshopPath("a/{{BAD}}.ini", v), undefined);
  assert.equal(workshopPath("a/{{EMPTY}}.ini", v), undefined);
  assert.equal(workshopPath("plain.ini", v), "plain.ini");
});

const zomboid = `# Comment
PVP=true
Mods=\\ModA;\\ModB
Map=Muldraugh, KY
WorkshopItems=111;222
Public=false
`;

test("ini lists are read and written in place", () => {
  assert.deepEqual(iniList(zomboid, "WorkshopItems"), ["111", "222"]);
  assert.deepEqual(iniList(zomboid, "Mods"), ["\\ModA", "\\ModB"]);
  assert.equal(iniList(zomboid, "Missing"), undefined);
  assert.deepEqual(iniList("WorkshopItems=\n", "WorkshopItems"), []);
  const out = setIniList(zomboid, "WorkshopItems", ["222", "333"]);
  assert.equal(out, zomboid.replace("WorkshopItems=111;222", "WorkshopItems=222;333"));
  assert.equal(setIniList("A=1", "B", ["x"]), "A=1\nB=x\n");
  assert.equal(setIniList("A=1\r\n", "B", ["x"]), "A=1\r\nB=x\r\n");
  // A key that's only a prefix of another isn't it.
  assert.deepEqual(iniList("ModsX=1\nMods=2\n", "Mods"), ["2"]);
});

test("Zomboid mod IDs follow the file's style", () => {
  assert.equal(zomboidModId("ModC", ["\\ModA"]), "\\ModC");
  assert.equal(zomboidModId("\\ModC", ["ModA"]), "ModC");
  assert.equal(zomboidModId("ModC", []), "ModC");
  assert.equal(modInfoId("name=A mod\nid=BetterSorting\nposter=poster.png\n"), "BetterSorting");
  assert.equal(modInfoId("name=x\n"), undefined);
});

const unturned = `{
  "File_IDs": [
    1234567890123456789,
    2
  ],
  "Ignore_Children_File_IDs": [],
  "Query_Cache_Max_Age_Seconds": 600,
  "Note": "File_IDs: [9]"
}`;

test("json arrays are read as text and written in place", () => {
  assert.deepEqual(jsonIds(unturned, "File_IDs"), ["1234567890123456789", "2"]);
  assert.deepEqual(jsonIds(unturned, "Ignore_Children_File_IDs"), []);
  assert.equal(jsonIds(unturned, "Missing"), undefined);
  const out = setJsonIds(unturned, "File_IDs", ["2", "3"]);
  assert.equal(
    out,
    unturned.replace("[\n    1234567890123456789,\n    2\n  ]", "[\n    2,\n    3\n  ]"),
  );
  assert.deepEqual(JSON.parse(out).File_IDs, [2, 3]);
  assert.equal(setJsonIds(unturned, "File_IDs", []), out.replace("[\n    2,\n    3\n  ]", "[]"));
  // A key nested deeper isn't the top-level one.
  assert.equal(jsonIds('{"A": {"File_IDs": [1]}}', "File_IDs"), undefined);
  const added = setJsonIds('{\n  "Other": 1\n}', "File_IDs", ["5"]);
  assert.deepEqual(JSON.parse(added), { File_IDs: [5], Other: 1 });
  assert.deepEqual(JSON.parse(setJsonIds("{}", "File_IDs", ["5"])), { File_IDs: [5] });
  assert.throws(() => setJsonIds("[1]", "File_IDs", ["5"]));
});

const setup = `--There are two functions that will install mods, ServerModSetup and ServerModCollectionSetup. Put the calls to the functions in this file and they will be executed on boot.

--ServerModSetup takes a string of a specific mod's Workshop id. It will download and install the mod to your mod directory on boot.
\t--The Workshop id can be found at the end of the url to the mod's Workshop page.
\t--Example: http://steamcommunity.com/sharedfiles/filedetails/?id=350811795
\t--ServerModSetup("350811795")

--ServerModCollectionSetup takes a string of a specific mod's Workshop id. It will download all the mods in the collection and install them to the mod directory on boot.
\t--ServerModCollectionSetup("379114180")
ServerModCollectionSetup("379114180")
ServerModSetup("111")
ServerModSetup('222')
`;

test("DST's dedicated_server_mods_setup.lua", () => {
  assert.deepEqual(dstSetupIds(setup), ["111", "222"]);
  const out = setDstSetup(setup, ["222", "333"]);
  assert.deepEqual(dstSetupIds(out), ["222", "333"]);
  assert.equal(out, setup.replace('ServerModSetup("111")\n', "").concat('ServerModSetup("333")\n'));
  // The commented-out examples and the collection stay.
  assert.ok(out.includes('--ServerModSetup("350811795")'));
  assert.ok(out.includes('ServerModCollectionSetup("379114180")\nServerModSetup'));
  assert.equal(setDstSetup("", ["1"]), 'ServerModSetup("1")\n');
  assert.equal(setDstSetup(setup, ["111", "222"]), setup);
});

const overrides = `return {
  -- Global Positions
  ["workshop-378160973"]={
    configuration_options={
      ENABLEPINGS=true,
      SHOWPLAYERICONS="{ not a table }",
    },
    enabled=true
  },
  ["workshop-111"]={ enabled=true },
  ["local-mod"]={ enabled=false },
  ["workshop-222"]={ configuration_options={ x=[[a } b]] }, enabled=true }
}
`;

test("DST's modoverrides.lua is read", () => {
  assert.deepEqual(dstOverrideIds(overrides), ["378160973", "111", "222"]);
  assert.deepEqual(dstOverrideIds(""), []);
  assert.deepEqual(dstOverrideIds("return {}"), []);
  assert.throws(() => dstOverrideIds("print(1)"));
});

test("DST's modoverrides.lua keeps settings and turns new items on", () => {
  // Removing the last entry (no separator) and one in the middle.
  const out = setDstOverrides(overrides, ["378160973", "333"]);
  assert.deepEqual(dstOverrideIds(out), ["378160973", "333"]);
  assert.ok(out.includes("ENABLEPINGS=true"));
  assert.ok(out.includes('["local-mod"]={ enabled=false },'));
  assert.ok(!out.includes("workshop-111"));
  assert.ok(!out.includes("workshop-222"));
  assert.ok(out.endsWith('  ["workshop-333"]={ enabled=true },\n}\n'));
  // Nothing to change: the same text.
  assert.equal(setDstOverrides(overrides, ["378160973", "111", "222"]), overrides);
  // A last entry without a separator gets one before ours.
  const one = 'return {\n  ["workshop-1"]={ enabled=true }\n}\n';
  assert.equal(
    setDstOverrides(one, ["1", "2"]),
    'return {\n  ["workshop-1"]={ enabled=true },\n  ["workshop-2"]={ enabled=true },\n}\n',
  );
  assert.equal(setDstOverrides(one, []), "return {\n}\n");
  assert.equal(
    setDstOverrides("return {}", ["5"]),
    'return {\n  ["workshop-5"]={ enabled=true },\n}',
  );
  assert.equal(setDstOverrides("", ["5"]), 'return {\n  ["workshop-5"]={ enabled=true },\n}\n');
  // A comment inside the table that looks like an entry is left alone.
  const commented =
    'return {\n  -- ["workshop-9"]={ enabled=true },\n  ["workshop-1"]={ enabled=true },\n}\n';
  assert.deepEqual(dstOverrideIds(commented), ["1"]);
  assert.equal(setDstOverrides(commented, ["1"]), commented);
});
