// Run with `pnpm test` (node --test).
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  baseName,
  crumbs,
  formatSize,
  fromBase64,
  isArchive,
  join,
  languageFor,
  looksBinary,
  parent,
  readOnlyByName,
  validName,
} from "./files.ts";

test("paths", () => {
  assert.equal(join("", "a"), "a");
  assert.equal(join("a/b", "c"), "a/b/c");
  assert.equal(parent("a/b/c"), "a/b");
  assert.equal(parent("a"), "");
  assert.equal(baseName("a/b/c.txt"), "c.txt");
  assert.deepEqual(crumbs("plugins/Essentials"), [
    { name: "plugins", path: "plugins" },
    { name: "Essentials", path: "plugins/Essentials" },
  ]);
  assert.deepEqual(crumbs(""), []);
});

test("sizes", () => {
  assert.equal(formatSize(512), "512 B");
  assert.equal(formatSize(1536), "1.5 KB");
  assert.equal(formatSize(50 * 2 ** 20), "50 MB");
  assert.equal(formatSize(3 * 2 ** 30), "3.0 GB");
});

test("binary, archives, languages, names", () => {
  assert.equal(looksBinary(new TextEncoder().encode("motd=hi\n")), false);
  assert.equal(looksBinary(new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0x00])), true);
  assert.ok(isArchive("world.tar.gz") && isArchive("Backup.ZIP") && !isArchive("server.jar"));
  assert.equal(languageFor("server.properties"), "ini");
  assert.equal(languageFor("config.YML"), "yaml");
  assert.equal(languageFor("ops.json"), "json");
  assert.equal(languageFor("start.sh"), "shell");
  assert.equal(languageFor("README"), "plaintext");
  assert.equal(readOnlyByName("logs/latest.log"), true);
  assert.equal(readOnlyByName("blog.txt"), false);
  assert.ok(validName("a.txt") && !validName("..") && !validName("a/b") && !validName(""));
});

test("base64 like Go's []byte", () => {
  assert.deepEqual(fromBase64("ZWdn"), new TextEncoder().encode("egg"));
});
