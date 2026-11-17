import assert from "node:assert/strict";
import { test } from "node:test";

import { Code, ConnectError } from "@connectrpc/connect";

import { unreachable } from "./connection.ts";

test("not reaching the Panel counts", () => {
  // fetch failing, bare and as connect wraps it.
  assert.equal(unreachable(new TypeError("Failed to fetch")), true);
  assert.equal(unreachable(ConnectError.from(new TypeError("Load failed"))), true);
  assert.equal(
    unreachable(
      ConnectError.from(new TypeError("NetworkError when attempting to fetch resource.")),
    ),
    true,
  );
  // A proxy's error page, and the client's own timeout.
  assert.equal(unreachable(new ConnectError("HTTP 502", Code.Unavailable)), true);
  assert.equal(unreachable(new ConnectError("HTTP 522", Code.Unknown)), true);
  assert.equal(
    unreachable(new ConnectError("the operation timed out", Code.DeadlineExceeded)),
    true,
  );
});

test("the Panel answering doesn't count", () => {
  // A node being offline is the Panel answering.
  assert.equal(unreachable(new ConnectError("node is offline", Code.Unavailable)), false);
  assert.equal(
    unreachable(
      new ConnectError("the node isn't connected to this instance any more", Code.Unavailable),
    ),
    false,
  );
  assert.equal(
    unreachable(new ConnectError("context deadline exceeded", Code.DeadlineExceeded)),
    false,
  );
  assert.equal(unreachable(new ConnectError("internal error", Code.Internal)), false);
  // Navigating away cancels requests.
  assert.equal(unreachable(new ConnectError("aborted", Code.Canceled)), false);
});

test("a bug in the app doesn't count", () => {
  assert.equal(
    unreachable(new TypeError("Cannot read properties of undefined (reading 'id')")),
    false,
  );
  assert.equal(unreachable(ConnectError.from(new TypeError("x is not a function"))), false);
});
