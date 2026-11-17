import assert from "node:assert/strict";
import { test } from "node:test";

import { demoSigned, demoState, isGuest, refuseSigning, setDemoGuest, showBanner } from "./demo.ts";

test("the demo page's states", () => {
  const base = { open: true, signedIn: "none" as const, starting: false, error: "" };
  assert.equal(demoState({ ...base, open: undefined }).kind, "checking");
  assert.equal(demoState({ ...base, open: false }).kind, "closed");
  assert.equal(demoState(base).kind, "ready");
  assert.equal(demoState({ ...base, starting: true }).kind, "starting");
  assert.deepEqual(demoState({ ...base, error: "Too many attempts" }), {
    kind: "failed",
    message: "Too many attempts",
  });
  // A real account isn't swapped for a guest, open or not.
  assert.equal(demoState({ ...base, open: false, signedIn: "account" }).kind, "signed-in");
  // A guest can start again (a new hour).
  assert.equal(demoState({ ...base, signedIn: "guest" }).kind, "ready");
});

test("who sees the banner", () => {
  assert.equal(showBanner(true, undefined), true);
  assert.equal(showBanner(false, true), true);
  assert.equal(showBanner(false, false), false);
  assert.equal(showBanner(false, undefined), false);
  assert.equal(isGuest({ guestUntil: {} }), true);
  assert.equal(isGuest({}), false);
  assert.equal(isGuest(undefined), false);
});

test("guests never get a passkey prompt", () => {
  setDemoGuest(false);
  assert.doesNotThrow(refuseSigning);
  setDemoGuest(true);
  assert.throws(refuseSigning, { message: demoSigned });
  setDemoGuest(false);
});
