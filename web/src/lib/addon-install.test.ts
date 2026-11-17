import assert from "node:assert/strict";
import { test } from "node:test";

import { addonNames, fileOf, modLoaderOff } from "./addon-install.ts";

const file = {
  url: "https://thunderstore.io/package/download/ValheimModding/Jotunn/2.30.2/",
  sha512: "",
  sha256: "",
  sha1: "",
  filename: "ValheimModding-Jotunn-2.30.2.zip",
};

test("Thunderstore's files are hashed by the Panel first", () => {
  assert.deepEqual(fileOf(file, "thunderstore", "ValheimModding-Jotunn-2.30.2"), {
    url: "",
    filename: "ValheimModding-Jotunn-2.30.2.zip",
    hashVersion: "ValheimModding-Jotunn-2.30.2",
  });
});

test("other sources' files carry their own hash", () => {
  const f = { ...file, url: "https://cdn.modrinth.com/x.jar", sha512: "abc", filename: "x.jar" };
  assert.deepEqual(fileOf(f, "modrinth", "v1"), {
    url: "https://cdn.modrinth.com/x.jar",
    sha512: "abc",
    sha256: undefined,
    sha1: undefined,
    filename: "x.jar",
  });
});

test("files are named by what each source looks up", () => {
  const files = [
    { name: "ValheimModding-Jotunn", sha512: "h1", package: "ValheimModding-Jotunn-2.20.0" },
    { name: "ByHand" },
    { name: "Kits.cs", sha512: "h2" },
  ];
  assert.deepEqual(addonNames("thunderstore", files), { h1: "ValheimModding-Jotunn-2.20.0" });
  assert.deepEqual(addonNames("umod", files), { h1: "ValheimModding-Jotunn", h2: "Kits.cs" });
  assert.deepEqual(addonNames("plugins", files), {});
});

test("a vanilla server doesn't load Thunderstore's mods", () => {
  assert.equal(modLoaderOff("thunderstore", { MOD_LOADER: "vanilla" }), true);
  assert.equal(modLoaderOff("thunderstore", { MOD_LOADER: "bepinex" }), false);
  assert.equal(modLoaderOff("thunderstore", { MOD_LOADER: "valheimplus" }), false);
  assert.equal(modLoaderOff("mods", { MOD_LOADER: "vanilla" }), false);
});
