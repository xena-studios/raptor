import assert from "node:assert/strict";
import { test } from "node:test";

import {
  actDef,
  cleanPath,
  contextOf,
  docsItem,
  eggItems,
  filesSearch,
  inviteCheck,
  nodeItems,
  type OrgInfo,
  orgActions,
  pageItems,
  pickable,
  pickItems,
  type ServerInfo,
  serverItem,
  serverItems,
  tabsOf,
} from "./command-items.ts";

function server(over: Partial<ServerInfo> = {}): ServerInfo {
  return {
    id: "s1",
    name: "Survival",
    nodeId: "n1",
    nodeName: "node-1",
    nodeConnected: true,
    egg: "Minecraft: Paper",
    address: "play.example.com",
    state: "running",
    installState: "",
    status: "Running",
    permissions: ["*"],
    playerLists: true,
    rconPlayers: "",
    ...over,
  };
}

const acts = (items: { id: string }[]) =>
  items.filter((i) => i.id.startsWith("act:")).map((i) => i.id.split(":")[1]);
const tabs = (items: { id: string }[]) =>
  items.filter((i) => i.id.startsWith("tab:")).map((i) => i.id.split(":")[1]);

const admin: OrgInfo = { id: "o1", name: "Org", admin: true };
const member: OrgInfo = { id: "o1", name: "Org", admin: false };

test("an admin gets every action that fits the server's state", () => {
  const got = acts(serverItems("o1", server()));
  for (const a of [
    "restart",
    "stop",
    "kill",
    "command",
    "backup",
    "whitelist",
    "op",
    "kick",
    "files",
  ]) {
    assert.ok(got.includes(a), a);
  }
  assert.ok(!got.includes("start"), "it's already running");
  assert.ok(!got.includes("dashboard"), "unknown without the server's config");
  const stopped = acts(serverItems("o1", server({ state: "offline" })));
  assert.ok(stopped.includes("start"));
  for (const a of ["stop", "kill", "command", "kick"]) assert.ok(!stopped.includes(a), a);
  assert.ok(stopped.includes("whitelist"), "a stopped server's files are edited");
});

test("actions follow the user's permissions on the server", () => {
  const none = acts(serverItems("o1", server({ permissions: [] })));
  assert.deepEqual(none, ["address", "test-connection"]);
  const power = acts(serverItems("o1", server({ permissions: ["power"] })));
  assert.ok(power.includes("restart") && power.includes("stop"));
  assert.ok(!power.includes("command") && !power.includes("backup") && !power.includes("files"));
  const players = acts(serverItems("o1", server({ permissions: ["players"] })));
  assert.ok(players.includes("whitelist") && players.includes("kick"));
  // Without players: console commands while running, its files when stopped.
  const console = acts(serverItems("o1", server({ permissions: ["console.write"] })));
  assert.ok(console.includes("whitelist") && console.includes("command"));
  const consoleStopped = acts(
    serverItems("o1", server({ permissions: ["console.write"], state: "offline" })),
  );
  assert.ok(!consoleStopped.includes("whitelist"));
});

test("the menu shows what the page shows: a permission taken away hides it", () => {
  // The demo org (#449) takes permissions away; nothing here knows it.
  const s = server({ permissions: ["console.read", "files.read"] });
  const items = serverItems("o1", s);
  assert.deepEqual(acts(items).sort(), [
    "address",
    "console",
    "files",
    "share-log",
    "test-connection",
  ]);
  assert.deepEqual(tabs(items), ["overview", "players", "files", "network"]);
});

test("nothing touches a server whose node is offline, or one mid-install", () => {
  const offline = acts(serverItems("o1", server({ nodeConnected: false })));
  for (const a of ["start", "restart", "stop", "kill", "command", "backup", "schedule", "addons"]) {
    assert.ok(!offline.includes(a), a);
  }
  const installing = acts(serverItems("o1", server({ installState: "installing", state: "" })));
  assert.ok(!installing.includes("restart") && !installing.includes("kill"));
  // Kill is there for a server stuck stopping.
  assert.ok(acts(serverItems("o1", server({ state: "stopping" }))).includes("kill"));
});

test("player actions are for Minecraft Java servers only", () => {
  const rcon = acts(serverItems("o1", server({ playerLists: false, rconPlayers: "rust" })));
  assert.ok(!rcon.includes("whitelist") && !rcon.includes("kick"));
  const other = acts(serverItems("o1", server({ playerLists: false })));
  assert.ok(!other.includes("op"));
});

test("the server you're on adds what its config knows", () => {
  const s = server();
  const extras = {
    game: true,
    addons: true,
    addonKinds: false,
    players: true,
    worlds: true,
    dashboard: { name: "txAdmin", url: "http://h:40120/" },
  };
  const items = serverItems("o1", s, { extras, own: true });
  const got = acts(items);
  assert.ok(got.includes("dashboard"));
  assert.ok(!got.includes("addons"), "its add-ons have no installer");
  assert.deepEqual(tabs(items).slice(0, 5), ["overview", "game", "addons", "players", "worlds"]);
  // Its own list says "Restart", not "Restart Survival".
  assert.equal(items.find((i) => i.id === "act:restart:s1")?.label, "Restart");
  const dash = items.find((i) => i.id === "act:dashboard:s1");
  assert.deepEqual(dash?.run, { kind: "open", url: "http://h:40120/" });
});

test("tabs match the tab bar's for a member", () => {
  const s = server({ permissions: ["files.read", "backups"] });
  assert.deepEqual(
    tabsOf(s).map((t) => t.key),
    ["overview", "players", "files", "backups", "network"],
  );
});

test("labels name the server, and items are found by its egg and node", () => {
  const items = serverItems("o1", server());
  const restart = items.find((i) => i.id === "act:restart:s1");
  assert.equal(restart?.label, "Restart Survival");
  assert.ok(restart?.keywords?.includes("Survival"));
  const s = serverItem("o1", server());
  assert.deepEqual(s.drill, { kind: "server", serverId: "s1" });
  assert.ok(s.keywords?.includes("Minecraft: Paper") && s.keywords.includes("node-1"));
  assert.equal(actDef("kill").confirm?.destructive, true);
  assert.equal(actDef("stop").confirm?.destructive, false);
});

test("pick items appear only when some server allows them", () => {
  const ids = pickItems([server({ permissions: ["power"] })]).map((i) => i.id);
  assert.ok(ids.includes("pick:restart"));
  assert.ok(!ids.includes("pick:command") && !ids.includes("pick:dashboard"));
  const servers = [server(), server({ id: "s2", permissions: [] })];
  assert.deepEqual(
    pickable("restart", servers).map((s) => s.id),
    ["s1"],
  );
});

test("pages follow the org role and staff", () => {
  const ids = (o: Parameters<typeof pageItems>[0]) => pageItems(o).map((i) => i.id);
  const base = { orgs: [admin], staff: false, docsUrl: "d", discordUrl: "x" };
  const asAdmin = ids({ ...base, org: admin });
  const asMember = ids({ ...base, org: member });
  for (const p of ["page:alerts", "page:databases", "page:audit", "page:new-node"]) {
    assert.ok(asAdmin.includes(p), p);
    assert.ok(!asMember.includes(p), p);
  }
  assert.ok(asMember.includes("page:members") && asMember.includes("page:billing"));
  assert.ok(!asAdmin.includes("page:admin"));
  assert.ok(ids({ ...base, org: admin, staff: true }).includes("page:admin-users"));
  const other = ids({ ...base, org: admin, orgs: [admin, { id: "o2", name: "B", admin: false }] });
  assert.ok(other.includes("org:o2") && !other.includes("org:o1"));
});

test("only admins create servers, invite, and deploy to nodes", () => {
  assert.deepEqual(orgActions(member), []);
  assert.deepEqual(
    orgActions(admin).map((i) => i.id),
    ["new-server", "invite"],
  );
  const node = { id: "n1", name: "node-1", hostname: "n1.example.com", connected: true };
  const asMember = nodeItems(member, node, { own: true }).map((i) => i.label);
  assert.deepEqual(asMember, ["Overview", "Copy the hostname"]);
  assert.ok(nodeItems(admin, node).some((i) => i.id === "deploy:n1"));
  const egg = {
    id: "minecraft/paper",
    name: "Paper",
    category: "minecraft",
    categoryName: "Minecraft",
  };
  assert.equal(eggItems(member, egg).length, 1);
  assert.deepEqual(eggItems(admin, egg)[1]?.run, {
    kind: "go",
    to: "/orgs/$orgId/servers/new",
    params: { orgId: "o1" },
    search: { egg: "minecraft/paper" },
  });
});

test("docs search carries the query", () => {
  assert.deepEqual(docsItem("https://docs.raptorpanel.net", " sftp access ").run, {
    kind: "open",
    url: "https://docs.raptorpanel.net/?q=sftp%20access",
  });
});

test("typed paths open Files at a folder or a file", () => {
  assert.equal(cleanPath(" /plugins//./Essentials/ "), "plugins/Essentials");
  assert.deepEqual(filesSearch("plugins"), { path: "plugins" });
  assert.deepEqual(filesSearch("/logs/latest.log"), { path: "logs", file: "logs/latest.log" });
  assert.deepEqual(filesSearch("server.properties"), { file: "server.properties" });
  assert.deepEqual(filesSearch(".config"), { path: ".config" });
  assert.deepEqual(filesSearch("/"), {});
});

test("inputs are checked before they're sent", () => {
  const name = actDef("whitelist").input;
  assert.equal(name?.check("Steve_1"), undefined);
  assert.ok(name?.check("not a name!"));
  assert.ok(name?.check(""));
  assert.ok(actDef("command").input?.check("  "));
  assert.equal(inviteCheck("a@b.co"), undefined);
  assert.ok(inviteCheck("nope"));
});

test("the page's context is read from its path", () => {
  assert.deepEqual(contextOf("/orgs/abc/servers/1cdd002cd9a3/files"), {
    kind: "server",
    ref: "1cdd002cd9a3",
  });
  assert.deepEqual(contextOf("/orgs/abc/nodes/ff00"), { kind: "node", ref: "ff00" });
  assert.equal(contextOf("/orgs/abc/servers/new"), undefined);
  assert.equal(contextOf("/orgs/abc/servers"), undefined);
  assert.equal(contextOf("/settings"), undefined);
});
