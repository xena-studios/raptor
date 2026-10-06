// Run with `pnpm test` (node --test). The vector is the Go one in
// internal/shared/nodecmd/envelope_test.go: both must agree, or passkey
// signatures made in the browser won't verify on nodes.
import assert from "node:assert/strict";
import { test } from "node:test";

import {
  canonicalJSON,
  commandCanonical,
  commandHash,
  keyFingerprint,
  uuidv7,
} from "./canonical.ts";

const vector =
  '{"action":"server.delete","command_id":"01a1125e-1097-7b27-a780-c4fc2b1e415f","expires_at":1791313200,"node_id":"01a10f2a-865c-74ec-9578-3fe2ea243894","params":{"final_backup":true,"note":"é \\"x\\" <y>","z":[1,2.5,"a"]},"server_id":"srv","user_id":"01a1128b-8f10-7828-94ea-ff8c157dca3d"}';

const fields = {
  action: "server.delete",
  commandId: "01a1125e-1097-7b27-a780-c4fc2b1e415f",
  expiresAt: 1791313200,
  nodeId: "01a10f2a-865c-74ec-9578-3fe2ea243894",
  params: { z: [1, 2.5, "a"], note: 'é "x" <y>', final_backup: true },
  serverId: "srv",
  userId: "01a1128b-8f10-7828-94ea-ff8c157dca3d",
};

test("the canonical form matches Go's", () => {
  assert.equal(commandCanonical(fields), vector);
});

test("the hash matches Go's", async () => {
  const h = Buffer.from(await commandHash(fields)).toString("hex");
  assert.equal(h, "4768e6502cd471428eb3064f7e9483f627dc637ac2762e72d4e9c044e6da50fe");
});

test("no params is {}", () => {
  assert.match(commandCanonical({ ...fields, params: undefined }), /"params":\{\},/);
});

test("keys sort by UTF-16 code units, nested too", () => {
  assert.equal(
    canonicalJSON({ b: 1, a: { d: [], c: null }, A: "x" }),
    '{"A":"x","a":{"c":null,"d":[]},"b":1}',
  );
  assert.throws(() => canonicalJSON({ x: Number.NaN }));
});

test("uuidv7 has the version and time", () => {
  const id = uuidv7(0x0190_0000_0000);
  assert.match(id, /^01900000-0000-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
});

test("fingerprints match Wings' KeyFingerprint", async () => {
  // command.KeyFingerprint([]byte{1, 2, 3}) in Go.
  assert.equal(await keyFingerprint(new Uint8Array([1, 2, 3])), "AOIF-RRXS-YDFU-SLCT-HMFE");
});
