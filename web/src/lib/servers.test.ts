// Run with `pnpm test` (node --test).
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  base64,
  changesCode,
  createParams,
  eggsFor,
  missingRequired,
  serverStatus,
  suggestMemoryMiB,
  suggestPort,
  updateParams,
} from "./servers.ts";

test("eggs a node can run, certified first", () => {
  const eggs = [
    { id: "b", name: "Bravo", category: "x", certified: false, arch: ["amd64", "arm64"] },
    { id: "a", name: "Alpha", category: "x", certified: false, arch: ["amd64"] },
    { id: "c", name: "Charlie", category: "x", certified: true, arch: ["arm64"] },
  ];
  assert.deepEqual(
    eggsFor(eggs, "arm64").map((e) => e.id),
    ["c", "b"],
  );
  assert.equal(eggsFor(eggs, "").length, 3);
  // An imported egg that doesn't say which CPUs it runs on.
  const unknown = { id: "d", name: "Delta", category: "imported", certified: false, arch: [] };
  assert.deepEqual(
    eggsFor([...eggs, unknown], "arm64").map((e) => e.id),
    ["c", "b", "d"],
  );
});

test("the usual port, or the next free one", () => {
  assert.equal(suggestPort("minecraft", []), 25565);
  assert.equal(suggestPort("minecraft", [25565, 25566]), 25567);
  assert.equal(suggestPort("steam", [25565]), 27015);
});

test("memory fits the node", () => {
  assert.equal(suggestMemoryMiB("minecraft", 8 * 2 ** 30), 2048);
  assert.equal(suggestMemoryMiB("steam", 2 * 2 ** 30), 1536);
  assert.equal(suggestMemoryMiB("minecraft", 0), 2048);
  assert.equal(suggestMemoryMiB("minecraft", 256 * 2 ** 20), 256);
});

test("required variables left empty", () => {
  const vars = [
    { name: "Token", env: "TOKEN", default: "", userEditable: true, rules: ["required", "string"] },
    { name: "Hidden", env: "H", default: "", userEditable: false, rules: ["required"] },
    { name: "Version", env: "V", default: "latest", userEditable: true, rules: ["required"] },
  ];
  assert.deepEqual(
    missingRequired(vars, {}).map((v) => v.env),
    ["TOKEN"],
  );
  assert.deepEqual(missingRequired(vars, { TOKEN: "x" }), []);
  assert.deepEqual(
    missingRequired(vars, { TOKEN: "x", V: "  " }).map((v) => v.env),
    ["V"],
  );
});

test("base64 like Go, for big eggs too", () => {
  assert.equal(base64(new TextEncoder().encode("egg")), "ZWdn");
  const big = new Uint8Array(100_000).map((_, i) => i % 251);
  assert.equal(base64(big), Buffer.from(big).toString("base64"));
});

test("create params send only changed variables", () => {
  const p = createParams(
    {
      name: " Survival ",
      eggId: "minecraft/paper",
      egg: new TextEncoder().encode("{}"),
      image: "img",
      variables: { V: "latest", B: "123" },
      port: 25565,
      memoryMiB: 2048,
      diskMiB: 0,
      acceptEula: false,
    },
    { V: "latest", B: "" },
  );
  assert.equal(p.name, "Survival");
  assert.deepEqual(p.variables, { B: "123" });
  assert.equal(p.accept_eula, undefined);
  assert.deepEqual(p.allocations, [{ ip: "0.0.0.0", port: 25565, primary: true }]);
});

test("server states", () => {
  const s = (state: string, installState = "installed", installError = "") => ({
    state,
    installState,
    installError,
  });
  assert.equal(serverStatus(s("running"), true).tone, "live");
  assert.equal(serverStatus(s("installing", "installing"), true).label, "Installing");
  assert.equal(serverStatus(s("offline", "pending"), true).tone, "pending");
  const failed = serverStatus(s("install_failed", "failed", "exit 1"), true);
  assert.equal(failed.tone, "failed");
  assert.equal(failed.detail, "exit 1");
  assert.equal(serverStatus(s("offline"), true).label, "Stopped");
  const stale = serverStatus(s("running"), false);
  assert.equal(stale.tone, "stale");
  assert.equal(stale.label, "Running");
});

// The same vector as internal/wings/actions/create_test.go: Wings must
// decode what the passkey signs into exactly this server.
test("create params match the Go vector", () => {
  const p = createParams(
    {
      name: "Survival",
      eggId: "minecraft/paper",
      egg: new TextEncoder().encode('{"x":1}'),
      image: "ghcr.io/x/java:25",
      variables: { MINECRAFT_VERSION: "1.21.10", BUILD_NUMBER: "latest" },
      port: 25565,
      memoryMiB: 2048,
      diskMiB: 10240,
      acceptEula: true,
    },
    { MINECRAFT_VERSION: "latest", BUILD_NUMBER: "latest" },
  );
  assert.equal(
    JSON.stringify(p),
    '{"name":"Survival","egg":"eyJ4IjoxfQ==","egg_source":"minecraft/paper","image":"ghcr.io/x/java:25","variables":{"MINECRAFT_VERSION":"1.21.10"},"limits":{"memory_mib":2048,"disk_mib":10240},"allocations":[{"ip":"0.0.0.0","port":25565,"primary":true}],"start_after_install":true,"accept_eula":true}',
  );
});

test("update params keep everything not edited, and no egg", () => {
  const cfg = {
    image: "java:21",
    startup: "java -jar server.jar",
    variables: { A: "1", B: "2" },
    limits: { memory_mib: 2048, disk_mib: 0, cpu_weight: 512 },
    settings: { crash_auto_restart: true },
    host_network: false,
    allocations: [
      { ip: "0.0.0.0", port: 25565, primary: true },
      { ip: "0.0.0.0", port: 25575 },
    ],
    egg_hash: "abc",
    egg: { images: [], variables: [] },
  };
  const change = {
    name: " New ",
    image: "java:21",
    variables: { B: "3" },
    memoryMiB: 4096,
    diskMiB: 10240,
    port: 25566,
  };
  const p = updateParams(cfg, change);
  assert.equal("egg" in p || "egg_hash" in p, false);
  assert.equal(p.name, "New");
  assert.deepEqual(p.variables, { A: "1", B: "3" });
  assert.deepEqual(p.limits, { memory_mib: 4096, disk_mib: 10240, cpu_weight: 512 });
  assert.deepEqual(p.allocations, [
    { ip: "0.0.0.0", port: 25566, primary: true },
    { ip: "0.0.0.0", port: 25575 },
  ]);
  assert.equal(p.startup, "java -jar server.jar");
  assert.equal(changesCode(cfg, change), false);
  assert.equal(changesCode(cfg, { ...change, image: "java:25" }), true);
});
