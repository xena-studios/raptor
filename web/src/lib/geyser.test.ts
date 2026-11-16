import assert from "node:assert/strict";
import { test } from "node:test";

import { geyserConfig, geyserPlatform, hasSamePort, samePort } from "./geyser.ts";

const plugin = `bedrock:
  # The port that will listen for connections
  port: 19132
  clone-remote-port: false
  motd1: "Geyser"
remote:
  address: auto
  port: 25565
`;

test("which Geyser a server takes", () => {
  assert.equal(geyserPlatform("plugins", ["purpur", "paper"]), "Spigot");
  assert.equal(geyserPlatform("plugins", ["velocity"]), "Velocity");
  assert.equal(geyserPlatform("plugins", ["waterfall", "bungeecord"]), "BungeeCord");
  assert.equal(geyserPlatform("mods", ["fabric"]), "Fabric");
  assert.equal(geyserPlatform("mods", ["forge"]), undefined);
  assert.equal(geyserPlatform("plugins", ["sponge"]), undefined);
  assert.equal(geyserConfig("Spigot", "plugins"), "plugins/Geyser-Spigot/config.yml");
  assert.equal(geyserConfig("Fabric", "mods"), "config/Geyser-Fabric/config.yml");
});

test("Bedrock on the server's own port", () => {
  // Plugins: clone the Java port, and nothing outside bedrock changes.
  const on = samePort(plugin, "Spigot", 25565);
  assert.equal(on, plugin.replace("clone-remote-port: false", "clone-remote-port: true"));
  assert.ok(hasSamePort(on ?? "", "Spigot", 25565));
  assert.ok(!hasSamePort(plugin, "Spigot", 25565));
  // A config without the key gets it, in the bedrock section.
  const bare = "bedrock:\n  port: 19132\nremote:\n  port: 25565\n";
  assert.equal(
    samePort(bare, "Spigot", 1),
    "bedrock:\n  port: 19132\n  clone-remote-port: true\nremote:\n  port: 25565\n",
  );
  // Mods: the port itself, not remote's.
  const mod = samePort(plugin, "Fabric", 25601) ?? "";
  assert.match(mod, /bedrock:\n {2}# The port that will listen for connections\n {2}port: 25601/);
  assert.match(mod, /remote:\n {2}address: auto\n {2}port: 25565/);
  // Not Geyser's config.
  assert.equal(samePort("motd: hi\n", "Spigot", 1), undefined);
});
