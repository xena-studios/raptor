import assert from "node:assert/strict";
import { test } from "node:test";

import { change, frameworks, hasFrameworks, versionError } from "./cs2-frameworks.ts";

const defaults = {
  METAMOD: "0",
  METAMOD_VERSION: "latest",
  COUNTERSTRIKESHARP: "0",
  CSS_VERSION: "latest",
};
const off = frameworks({}, defaults);

test("which eggs take the frameworks", () => {
  assert.ok(hasFrameworks(Object.keys(defaults).concat("SRCDS_MAP")));
  assert.ok(!hasFrameworks(["METAMOD", "SRCDS_MAP"]));
});

test("the server's choices over the egg's defaults", () => {
  assert.deepEqual(off, {
    metamod: false,
    metamodVersion: "latest",
    css: false,
    cssVersion: "latest",
  });
  const f = frameworks({ COUNTERSTRIKESHARP: "true", CSS_VERSION: "1.0.380" }, defaults);
  assert.equal(f.css, true);
  assert.equal(f.cssVersion, "1.0.380");
  assert.equal(frameworks({ METAMOD_VERSION: "" }, defaults).metamodVersion, "latest");
});

test("versions the egg accepts", () => {
  assert.equal(versionError("metamod", "latest"), "");
  assert.equal(versionError("metamod", "1491"), "");
  assert.notEqual(versionError("metamod", "2.0.0-git1491"), "");
  assert.equal(versionError("css", "v1.0.380"), "");
  assert.equal(versionError("css", "1.0.380"), "");
  assert.notEqual(versionError("css", "1.0.380; rm"), "");
});

test("what a change sends, and when it needs a reinstall", () => {
  // Turning one on installs it.
  assert.deepEqual(change(off, { ...off, metamod: true }), {
    variables: { METAMOD: "1" },
    reinstall: true,
  });
  // CounterStrikeSharp brings Metamod: on with Metamod already on still installs.
  const mm = { ...off, metamod: true };
  assert.equal(change(mm, { ...mm, css: true }).reinstall, true);
  // Turning off is a restart.
  const both = { ...mm, css: true };
  assert.deepEqual(change(both, { ...both, css: false }), {
    variables: { COUNTERSTRIKESHARP: "0" },
    reinstall: false,
  });
  assert.equal(change(both, off).reinstall, false);
  // Metamod off while CounterStrikeSharp stays on: it still loads, nothing to install.
  assert.equal(change(both, { ...both, metamod: false }).reinstall, false);
  // A new version of one that's on installs; of one that's off doesn't.
  assert.equal(change(both, { ...both, cssVersion: "1.0.380" }).reinstall, true);
  assert.equal(change(off, { ...off, cssVersion: "1.0.380" }).reinstall, false);
  assert.equal(change(off, off).reinstall, false);
});
