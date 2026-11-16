import assert from "node:assert/strict";
import { test } from "node:test";

import {
  paperForVelocity,
  readBackends,
  readBungeeBackends,
  spigotForBungee,
  validBackendName,
  writeBackends,
  writeBungeeBackends,
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

const bungee = `server_connect_timeout: 5000
listeners:
- query_port: 25577
  motd: '&1Another Bungee server'
  priorities:
  - lobby
  bind_local_address: true
  host: 0.0.0.0:25577
ip_forward: false
servers:
  lobby:
    motd: '&1Just another BungeeCord - Forced Host'
    address: localhost:25565
    restricted: false

online_mode: true
`;

test("BungeeCord's servers and priorities", () => {
  const got = readBungeeBackends(bungee);
  assert.deepEqual(got, {
    servers: [{ name: "lobby", address: "localhost:25565" }],
    try: ["lobby"],
  });
  const out = writeBungeeBackends(
    bungee,
    [
      { name: "survival", address: "node1.example:30001" },
      { name: "lobby", address: "node1.example:30000" },
    ],
    ["survival", "lobby"],
  );
  assert.deepEqual(readBungeeBackends(out), {
    servers: [
      { name: "survival", address: "node1.example:30001" },
      { name: "lobby", address: "node1.example:30000" },
    ],
    try: ["survival", "lobby"],
  });
  assert.match(out, /^ip_forward: true$/m);
  assert.match(out, /^online_mode: true$/m);
  assert.match(out, /'&1Just another BungeeCord - Forced Host'/);
  assert.match(out, /^ {2}bind_local_address: true$/m);
  // Emptied, it's still valid YAML.
  assert.match(writeBungeeBackends(bungee, [], []), /^servers: \{\}$/m);
});

test("spigot.yml takes BungeeCord's forwarding", () => {
  const yml =
    "config-version: 12\nsettings:\n  debug: false\n  bungeecord: false\nworld-settings:\n  default: {}\n";
  assert.equal(spigotForBungee(yml), yml.replace("bungeecord: false", "bungeecord: true"));
  assert.match(spigotForBungee("settings:\n  debug: false\n") ?? "", /^ {2}bungeecord: true$/m);
  assert.equal(spigotForBungee("nothing: here\n"), undefined);
});
