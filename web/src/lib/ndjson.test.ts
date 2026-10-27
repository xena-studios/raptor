import assert from "node:assert/strict";
import { test } from "node:test";

import { readLines } from "./ndjson.ts";

function streamOf(chunks: string[]): ReadableStream<Uint8Array<ArrayBuffer>> {
  const enc = new TextEncoder();
  return new ReadableStream({
    start(c) {
      for (const s of chunks) c.enqueue(enc.encode(s));
      c.close();
    },
  });
}

test("lines split across chunks arrive whole, in order", async () => {
  const got: unknown[] = [];
  await readLines(
    streamOf([
      '{"query_id":"q',
      '1"}\n{"columns":[{"name":"id"}]}\n{"ro',
      'ws":[["1"],[null]]}\n\n',
    ]),
    (v) => got.push(v),
  );
  assert.deepEqual(got, [
    { query_id: "q1" },
    { columns: [{ name: "id" }] },
    { rows: [["1"], [null]] },
  ]);
});

test("a last line without its newline counts, and UTF-8 split mid-character survives", async () => {
  const got: unknown[] = [];
  const bytes = new TextEncoder().encode('{"rows":[["é"]]}\n{"done":{"rows":1}}');
  const enc: Uint8Array<ArrayBuffer>[] = [bytes.slice(0, 12), bytes.slice(12)]; // splits "é"'s two bytes
  await readLines(
    new ReadableStream({
      start(c) {
        for (const b of enc) c.enqueue(b);
        c.close();
      },
    }),
    (v) => got.push(v),
  );
  assert.deepEqual(got, [{ rows: [["é"]] }, { done: { rows: 1 } }]);
});
