import assert from "node:assert/strict";
import { test } from "node:test";

import { applyValues, formFields, invalid, minecraftFields, otherKeys } from "./game-settings.ts";

const props = `#Minecraft server properties
difficulty=easy
pvp=true
server-port=25565
motd=A Minecraft Server
view-distance=10
some-new-setting=on
`;

test("the form follows the file", () => {
  assert.deepEqual(
    formFields(props).map((f) => f.key),
    ["difficulty", "pvp", "view-distance"],
  );
  // The port is Raptor's, the MOTD has its own editor; the rest is "other".
  assert.deepEqual(otherKeys(props), ["some-new-setting"]);
});

test("checking and writing values", () => {
  const vd = minecraftFields.find((f) => f.key === "view-distance");
  const name = minecraftFields.find((f) => f.key === "level-name");
  assert.ok(vd && name);
  assert.equal(invalid(vd, "12"), "");
  assert.equal(invalid(vd, "40"), "Between 3 and 32.");
  assert.equal(invalid(vd, "ten"), "A whole number.");
  assert.notEqual(invalid(name, "../etc"), "");
  assert.equal(invalid(name, "survival 2"), "");
  const next = applyValues(props, { difficulty: "hard", pvp: "false" });
  assert.match(next, /^difficulty=hard$/m);
  assert.match(next, /^pvp=false$/m);
  // Everything else is as it was, in place.
  assert.equal(next.split("\n").length, props.split("\n").length);
  assert.match(next, /^some-new-setting=on$/m);
});
