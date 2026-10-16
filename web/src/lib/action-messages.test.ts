import assert from "node:assert/strict";
import { test } from "node:test";

import { describe } from "./action-messages.ts";

test("changes say what they did; reads and listed exceptions don't", () => {
  assert.equal(describe("OrgService.RenameOrg", {}), "Organization renamed");
  assert.equal(describe("AuthService.UpdateProfile", {}), "Profile saved");
  assert.equal(
    describe("CommandService.Execute", { action: "server.start" }),
    "Starting the server",
  );
  assert.equal(describe("OrgService.SetNodeSFTP", { allowed: false }), "SFTP stopped on this node");
  // Reads, node reads, sign-in steps, and the file manager's own.
  assert.equal(describe("OrgService.ListNodes", {}), undefined);
  assert.equal(describe("CommandService.Execute", { action: "node.metrics" }), undefined);
  assert.equal(describe("AuthService.StartEmailSignIn", {}), undefined);
  assert.equal(describe("CommandService.Execute", { action: "files.delete" }), undefined);
});
