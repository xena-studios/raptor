import assert from "node:assert/strict";
import { test } from "node:test";

import { serverAddress } from "./addresses.ts";

test("server addresses", () => {
  const n = { shortId: "1a2b3c4d5e6f" };
  // Without a shown address: the node's hostname, with the port.
  assert.deepEqual(serverAddress(n, { displayAddress: "" }, 25565), {
    text: "1a2b3c4d5e6f.raptornodes.net:25565",
    private: true,
  });
  // A shown address is shown as typed: no port added.
  assert.deepEqual(serverAddress(n, { displayAddress: "play.example.com" }, 25565), {
    text: "play.example.com",
    private: false,
  });
  assert.equal(
    serverAddress(n, { displayAddress: "mc.example.com:25565" }, 30001).text,
    "mc.example.com:25565",
  );
});
