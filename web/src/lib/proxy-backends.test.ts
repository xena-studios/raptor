import assert from "node:assert/strict";
import { test } from "node:test";

import {
  paperForVelocity,
  readBackends,
  validBackendName,
  writeBackends,
} from "./proxy-backends.ts";

const toml = `config-version = "2.7"
bind = "0.0.0.0:25565"
player-info-forwarding-mode = "NONE"

[servers]
lobby = "127.0.0.1:30066"
factions = "127.0.0.1:30067"
try = [
  "lobby"
]

[forced-hosts]
"lobby.example.com" = ["lobby"]
`;

test("reading and writing Velocity's backends", () => {
  assert.deepEqual(readBackends(toml), {
    servers: [
      { name: "lobby", address: "127.0.0.1:30066" },
      { name: "factions", address: "127.0.0.1:30067" },
    ],
    try: ["lobby"],
  });
  const next = writeBackends(
    toml,
    [{ name: "smp", address: "1a2b.raptornodes.net:25601" }],
    ["smp"],
  );
  assert.deepEqual(readBackends(next), {
    servers: [{ name: "smp", address: "1a2b.raptornodes.net:25601" }],
    try: ["smp"],
  });
  // Modern forwarding on; the rest of the file as it was.
  assert.match(next, /^player-info-forwarding-mode = "modern"$/m);
  assert.match(next, /^\[forced-hosts\]\n"lobby.example.com" = \["lobby"\]$/m);
  assert.match(next, /^bind = "0.0.0.0:25565"$/m);
  assert.ok(validBackendName("smp-2") && !validBackendName("a b") && !validBackendName('x"]'));
});

test("a Paper backend set to take players from Velocity", () => {
  const yml = `_version: 31
proxies:
  bungee-cord:
    online-mode: true
  proxy-protocol: false
  velocity:
    enabled: false
    online-mode: true
    secret: ''
scoreboards:
  save-empty: false
`;
  const out = paperForVelocity(yml, "s3cr'et") ?? "";
  assert.match(out, /^ {4}enabled: true$/m);
  assert.match(out, /^ {4}secret: 's3cr''et'$/m);
  // Bungee's online-mode and the rest stay.
  assert.match(out, /bungee-cord:\n {4}online-mode: true/);
  assert.match(out, /^scoreboards:\n {2}save-empty: false$/m);
  assert.equal(paperForVelocity("settings: {}\n", "x"), undefined);
});
