import assert from "node:assert/strict";
import { test } from "node:test";

import { logTail, shareableLog } from "./share-log.ts";

test("colour codes and line endings are cleaned up", () => {
  assert.equal(shareableLog("\x1b[33m[WARN]\x1b[0m hi\r\nthere\r"), "[WARN] hi\nthere\n");
});

test("the newest lines that fit", () => {
  assert.deepEqual(logTail("a\nb\nc\n"), { text: "a\nb\nc", lines: 3, cut: false });
  assert.deepEqual(logTail("aaaa\nbbbb\ncccc", 10), { text: "bbbb\ncccc", lines: 2, cut: true });
  assert.deepEqual(logTail(""), { text: "", lines: 0, cut: false });
  // Bytes, not characters.
  assert.equal(logTail("ééé\nx", 4).text, "x");
});
