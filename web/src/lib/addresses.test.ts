import assert from "node:assert/strict";
import { test } from "node:test";

import { hasPort, serverAddress } from "./addresses.ts";

test("server addresses", () => {
  const n = { shortId: "abcd2345" };
  const s = { displayAddress: "" };
  assert.deepEqual(serverAddress(n, s, 25565), {
    text: "n-abcd2345.raptornodes.net:25565",
    private: true,
  });
  assert.deepEqual(serverAddress(n, { displayAddress: "play.example.com" }, 25565), {
    text: "play.example.com:25565",
    private: false,
  });
  // A shown address with its own port (a proxy's) is shown as is.
  assert.equal(
    serverAddress(n, { displayAddress: "mc.example.com:25565" }, 30001).text,
    "mc.example.com:25565",
  );
  assert.equal(serverAddress(n, { displayAddress: "2001:db8::1" }, 7777).text, "2001:db8::1:7777");
  assert.equal(hasPort("[2001:db8::1]:7777"), true);
  assert.equal(hasPort("2001:db8::1"), false);
});
