import assert from "node:assert/strict";
import { test } from "node:test";

import { findProblem, javaImage, javaOf, type Problem } from "./problems.ts";

const java = (p?: Problem) => (p?.kind === "java" ? [p.need, p.orNewer] : undefined);

const images = [
  { name: "Java 25", ref: "ghcr.io/xena-studios/yolks:java_25@sha256:aa" },
  { name: "Java 21", ref: "ghcr.io/xena-studios/yolks:java_21@sha256:bb" },
  { name: "Java 17", ref: "ghcr.io/xena-studios/yolks:java_17@sha256:cc" },
  { name: "Java 8", ref: "ghcr.io/xena-studios/yolks:java_8@sha256:dd" },
];

test("the library eggs' Java refusals", () => {
  let p = findProblem([
    "container@raptor~ Server marked as starting...",
    "This Minecraft version needs Java 25 or newer, and the server runs Java 21. Choose Java 25 in Startup, then start the server again.",
  ]);
  assert.deepEqual(java(p), [25, false]);
  p = findProblem([
    "This server needs Java 17 or newer, but Java 8 is chosen. Choose Java 17 or newer in Startup, then start the server again.",
  ]);
  assert.deepEqual(java(p), [17, true]);
  p = findProblem([
    "This Minecraft version runs on Java 8 at most, and the server runs Java 17. Choose Java 8 in Startup, then start the server again.",
  ]);
  assert.deepEqual(java(p), [8, false]);
});

test("Java's own error for a newer class file", () => {
  const p = findProblem([
    '\x1b[31mException in thread "main" java.lang.UnsupportedClassVersionError: org/bukkit/craftbukkit/Main has been compiled by a more recent version of the Java Runtime (class file version 65.0), this version of the Java Runtime only recognizes class file versions up to 61.0\x1b[0m',
  ]);
  assert.deepEqual(java(p), [21, true]);
});

test("only the newest lines count", () => {
  const old = "Choose Java 21 in Startup, then start the server again.";
  const later = Array.from({ length: 300 }, (_, i) => `[Server thread/INFO]: line ${i}`);
  assert.equal(findProblem([old, ...later]), undefined);
  assert.equal(findProblem(["nothing to see"]), undefined);
});

test("picking the image", () => {
  assert.equal(javaOf({ name: "Java 21", ref: "" }), 21);
  assert.equal(javaOf({ name: "Default", ref: "eclipse-temurin:java-11" }), 11);
  assert.equal(javaOf({ name: "Latest", ref: "example/image:latest" }), 0);
  assert.equal(javaImage(images, 21, false)?.name, "Java 21");
  // Or newer: the oldest that's new enough.
  assert.equal(javaImage(images, 18, true)?.name, "Java 21");
  assert.equal(javaImage(images, 11, false), undefined);
  assert.equal(javaImage(images, 26, true), undefined);
});

test("known console errors", () => {
  const id = (line: string) => {
    const p = findProblem([line]);
    return p?.kind === "hint" ? p.id : p?.kind;
  };
  assert.equal(id("[raptor] crashed (exit code 137, oom)"), "oom");
  assert.equal(id('Exception in thread "main" java.lang.OutOfMemoryError: Java heap space'), "oom");
  assert.equal(id("[raptor] crashed (exit code 1, exit)"), undefined);
  assert.equal(id("[12:00:00 WARN]: **** FAILED TO BIND TO PORT!"), "port");
  assert.equal(id("Error: listen EADDRINUSE: address already in use :::3000"), "port");
  assert.equal(
    id(
      "[12:00:00 INFO]: You need to agree to the EULA in order to run the server. Go to eula.txt for more info.",
    ),
    "eula",
  );
  assert.equal(
    id(
      "\t - Mod 'Sodium Extra' (sodium-extra) 0.6.0 requires version 0.6.0 or later of sodium, which is missing!",
    ),
    "fabric-deps",
  );
  assert.equal(id("Missing or unsupported mandatory dependencies:"), "forge-deps");
  assert.equal(
    id(
      "[12:00:01 ERROR]: Could not load 'plugins/Shop.jar' in folder 'plugins' org.bukkit.plugin.UnknownDependencyException: Unknown/missing dependency plugins: [Vault]",
    ),
    "plugin-deps",
  );
  assert.equal(
    id(
      "[12:00:01 ERROR]: Could not load 'plugins/Old.jar' in folder 'plugins' org.bukkit.plugin.InvalidPluginException: Unsupported API version 1.22",
    ),
    "plugin-version",
  );
  assert.equal(
    id("[raptor] crashed 3 times in 10m0s; not restarting until someone starts it"),
    "crash-loop",
  );
  // The newest line wins.
  const p = findProblem([
    "[raptor] crashed (exit code 137, oom)",
    "This server needs Java 21 or newer, and it runs Java 17. Choose Java 21 in Startup, then start the server again.",
  ]);
  assert.equal(p?.kind, "java");
});
