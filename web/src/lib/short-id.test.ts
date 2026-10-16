import assert from "node:assert/strict";
import { test } from "node:test";

import { canonical, sameId, shortId } from "./short-id.ts";

const node = "01a14621-e529-7833-b8b3-1cdd002cd9a3";
const org = "01a145ff-0000-7000-8000-00000000beef";

test("short IDs", () => {
  assert.equal(shortId(node), "1cdd002cd9a3");
  assert.equal(shortId("1cdd002cd9a3"), "1cdd002cd9a3");
  assert.equal(sameId(node, "1cdd002cd9a3"), true);
  assert.equal(sameId(node, org), false);
});

test("old URLs get short IDs", () => {
  assert.equal(
    canonical(`/orgs/${org}/nodes/${node}/system?x=1`, { orgId: org, nodeId: node }),
    "/orgs/00000000beef/nodes/1cdd002cd9a3/system?x=1",
  );
  assert.equal(canonical("/orgs/00000000beef", { orgId: "00000000beef" }), undefined);
  // Other IDs, an egg's, stay whole.
  assert.equal(
    canonical(`/orgs/${org}/eggs/${node}`, { orgId: org, eggId: node }),
    `/orgs/00000000beef/eggs/${node}`,
  );
});
