import assert from "node:assert/strict";
import { test } from "node:test";

import { minehutEdits, minehutFlags, minehutKind, withFlags, withoutFlags } from "./minehut.ts";

test("which servers Minehut has steps for", () => {
  assert.equal(minehutKind(["velocity"]), "velocity");
  assert.equal(minehutKind(["waterfall", "bungeecord"]), "waterfall");
  assert.equal(minehutKind(["bungeecord", "waterfall"]), "bungeecord");
  assert.equal(minehutKind(["purpur", "paper", "spigot", "bukkit"]), "paper");
  assert.equal(minehutKind(["fabric"]), undefined);
  assert.equal(minehutKind(undefined), undefined);
});

test("putting the flags after java and taking them out", () => {
  const start =
    "cd x; read -r need < .raptor-java || need=8; exec java -Xms128M -jar {{SERVER_JARFILE}} nogui";
  const on = withFlags(start, minehutFlags.velocity);
  assert.equal(
    on,
    `cd x; read -r need < .raptor-java || need=8; exec java ${minehutFlags.velocity} -Xms128M -jar {{SERVER_JARFILE}} nogui`,
  );
  // Twice changes nothing; out again is the original.
  assert.equal(withFlags(on ?? "", minehutFlags.velocity), on);
  assert.equal(withoutFlags(on ?? "", minehutFlags.velocity), start);
  assert.equal(withFlags("java -jar x.jar", "-Da=b"), "java -Da=b -jar x.jar");
  // The library's eggs check the version first: the flags go on the launch.
  const library =
    "read -r need < .raptor-java || need=8; java -version 2>&1 | awk '{ print }' && exec java -Xms128M -jar server.jar nogui";
  assert.equal(
    withFlags(library, "-Da=b"),
    "read -r need < .raptor-java || need=8; java -version 2>&1 | awk '{ print }' && exec java -Da=b -Xms128M -jar server.jar nogui",
  );
  assert.equal(withFlags("java -version && ./start.sh", "-Da=b"), undefined);
  assert.equal(withFlags("./start.sh", "-Da=b"), undefined);
});

test("config edits", () => {
  const [velocity] = minehutEdits("velocity", false);
  assert.equal(velocity?.path, "velocity.toml");
  assert.equal(
    velocity?.apply("[advanced]\nhaproxy-protocol = false\nlogin-ratelimit = 3000\n", true),
    "[advanced]\nhaproxy-protocol = true\nlogin-ratelimit = 3000\n",
  );
  assert.equal(velocity?.apply('bind = "0.0.0.0:25577"\n', true), undefined);
  assert.equal(minehutEdits("velocity", true).length, 0);

  const [waterfall] = minehutEdits("waterfall", false);
  assert.equal(
    waterfall?.apply("listeners:\n- query_port: 25577\n  proxy_protocol: false\n", true),
    "listeners:\n- query_port: 25577\n  proxy_protocol: true\n",
  );

  const paper = minehutEdits("paper", false);
  assert.deepEqual(
    paper.map((e) => e.path),
    ["config/paper-global.yml", "server.properties"],
  );
  assert.equal(
    paper[0]?.apply("proxies:\n  proxy-protocol: false\n  velocity:\n    enabled: false\n", true),
    "proxies:\n  proxy-protocol: true\n  velocity:\n    enabled: false\n",
  );
  assert.match(
    paper[1]?.apply("motd=hi\nenforce-secure-profile=true\n", true) ?? "",
    /enforce-secure-profile=false/,
  );
  // Off again puts them back.
  assert.match(
    paper[1]?.apply("enforce-secure-profile=false\n", false) ?? "",
    /enforce-secure-profile=true/,
  );
  assert.deepEqual(
    minehutEdits("paper", true).map((e) => e.path),
    ["server.properties"],
  );
});
