import assert from "node:assert/strict";
import { test } from "node:test";

import { compareVersions, versionSoftware } from "./version-switch.ts";

test("which software a server runs", () => {
  const paperVars = ["MINECRAFT_VERSION", "BUILD_NUMBER", "SERVER_JARFILE"];
  assert.equal(versionSoftware("minecraft/paper", "", paperVars)?.source, "paper");
  assert.equal(versionSoftware("", "Minecraft: Purpur", paperVars)?.source, "purpur");
  assert.deepEqual(versionSoftware("minecraft/fabric", "", ["MC_VERSION"])?.reset, [
    "LOADER_VERSION",
    "FABRIC_VERSION",
  ]);
  // Forks keep Startup: their versions aren't Paper's.
  assert.equal(versionSoftware("minecraft/leaf", "Minecraft: Leaf", paperVars), undefined);
  // An egg without the variable (edited) doesn't get it.
  assert.equal(versionSoftware("minecraft/paper", "", ["SERVER_JARFILE"]), undefined);
});

test("ordering versions", () => {
  assert.ok(compareVersions("1.21.4", "1.21.10") < 0);
  assert.ok(compareVersions("26.3", "1.21.4") > 0);
  assert.ok(compareVersions("26.3", "26.3-rc-3") > 0);
  assert.equal(compareVersions("1.21", "1.21.0"), 0);
});
