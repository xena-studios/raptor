import assert from "node:assert/strict";
import { test } from "node:test";

import { sharedDatabases, throughServer } from "./db-shared.ts";

test("throughServer opens a database through its first linked server", () => {
  const [linked, alone, old] = throughServer<{ server_id: string; name: string; user: string }>({
    databases: [
      { id: "a", db_name: "lp", username: "lp", links: [{ server_id: "s2" }, { server_id: "s3" }] },
      { id: "b", db_name: "x", username: "x", links: [] },
      { id: "c", server_id: "s1", name: "s1_y", user: "s1_y" },
    ],
  });
  assert.equal(linked?.server_id, "s2");
  assert.equal(linked?.name, "lp");
  assert.equal(linked?.user, "lp");
  assert.equal(alone?.server_id, "");
  assert.equal(old?.server_id, "s1");
  assert.equal(old?.name, "s1_y");
});

test("sharedDatabases reads the new list, with its links", () => {
  const [d] = sharedDatabases({
    databases: [
      {
        id: "d1",
        instance_id: "i1",
        label: "luckperms",
        db_name: "luckperms",
        username: "luckperms",
        created_at: 10,
        links: [{ server_id: "s1", created_at: 11 }, { server_id: "s2" }],
      },
    ],
  });
  assert.deepEqual(d, {
    id: "d1",
    instance_id: "i1",
    label: "luckperms",
    db_name: "luckperms",
    username: "luckperms",
    created_at: 10,
    links: [
      { server_id: "s1", created_at: 11 },
      { server_id: "s2", created_at: 0 },
    ],
  });
});

test("sharedDatabases reads an older node's list: its one server is its link", () => {
  const [d] = sharedDatabases({
    databases: [
      {
        id: "d1",
        instance_id: "i1",
        server_id: "s1",
        label: "lp",
        name: "s0123456789ab_lp",
        user: "s0123456789ab_lp",
        created_at: 5,
      },
    ],
  });
  assert.equal(d?.db_name, "s0123456789ab_lp");
  assert.equal(d?.username, "s0123456789ab_lp");
  assert.deepEqual(d?.links, [{ server_id: "s1", created_at: 5 }]);
  assert.deepEqual(sharedDatabases(null), []);
  assert.deepEqual(sharedDatabases({}), []);
});
