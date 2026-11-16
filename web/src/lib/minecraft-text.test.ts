import assert from "node:assert/strict";
import { test } from "node:test";

import {
  escapeProperty,
  motdLines,
  plainLength,
  propertyValue,
  withProperty,
} from "./minecraft-text.ts";

const props = `#Minecraft server properties
#Mon Nov 16 12:00:00 UTC 2026
level-name=world
motd=\\u00A76Welcome\\u00A7r to \\u00A7lthe SMP\\nline two
server-port=25565
`;

test("reading a property", () => {
  assert.equal(propertyValue(props, "motd"), "§6Welcome§r to §lthe SMP\nline two");
  assert.equal(propertyValue(props, "level-name"), "world");
  assert.equal(propertyValue(props, "nope"), undefined);
  assert.equal(propertyValue("motd=", "motd"), "");
  assert.equal(propertyValue("a\\=b=c\\:d", "a"), undefined);
});

test("writing a property", () => {
  assert.equal(escapeProperty("§aHi: there\nyes"), "\\u00A7aHi\\: there\\nyes");
  assert.equal(escapeProperty(" lead"), "\\ lead");
  const next = withProperty(props, "motd", "§bNew");
  assert.equal(propertyValue(next, "motd"), "§bNew");
  assert.equal(propertyValue(next, "server-port"), "25565");
  assert.equal(next.split("\n").length, props.split("\n").length);
  // Missing: added at the end.
  assert.equal(withProperty("a=1", "motd", "x"), "a=1\nmotd=x\n");
  // Round trip with an emoji (two UTF-16 units).
  assert.equal(propertyValue(withProperty("", "motd", "hi 🎉"), "motd"), "hi 🎉");
});

test("drawing a MOTD", () => {
  const [one, two, three] = motdLines("§6Gold §lbold§r plain\n§cred\nthird");
  assert.deepEqual(one, [
    { colour: "#FFAA00", text: "Gold " },
    { colour: "#FFAA00", bold: true, text: "bold" },
    { colour: "#AAAAAA", text: " plain" },
  ]);
  assert.deepEqual(two, [{ colour: "#FF5555", text: "red" }]);
  assert.equal(three, undefined);
  // A colour resets bold.
  assert.equal(motdLines("§lB§aG")[0]?.[1]?.bold, undefined);
  assert.equal(plainLength("§6Gold §lbold"), 9);
});
