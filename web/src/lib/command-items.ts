// What the command menu offers (#436): the pages a user can open, their
// servers, nodes, eggs, and org members, and what they can do to each
// server, built from the same permission checks the pages use (a server's
// tabs come from lib/server-tabs, so the menu and the tab bar agree).
// Items are data: what to show, what it's found by, and what picking it
// does (CommandRun), which the menu carries out. Pure, so it's tested in
// node.

import { validName } from "./minecraft-players.ts";
import { type ServerTab, type ServerTabKey, serverTabs } from "./server-tabs.ts";

// What picking an item does.
export type CommandRun =
  // Open a page in the app.
  | { kind: "go"; to: string; params?: Record<string, string>; search?: Record<string, string> }
  // Open a page elsewhere (the docs, Discord, a server's web dashboard).
  | { kind: "open"; url: string }
  // Do something to a server: at once, after a confirm, or after an input
  // step (serverActs says which).
  | { kind: "act"; act: ServerAct; serverId: string }
  // Pick a server, then do act to it.
  | { kind: "pick"; act: ServerAct }
  // A server's, node's, or egg's own commands.
  | { kind: "server"; serverId: string }
  | { kind: "node"; nodeId: string }
  | { kind: "egg"; eggId: string }
  | { kind: "copy"; text: string; what: string }
  | { kind: "invite" }
  | { kind: "help" }
  // In a schedule step: run this one now.
  | { kind: "run-schedule"; serverId: string; scheduleId: string; name: string }
  // In an input step: do the act with what was typed.
  | { kind: "submit" };

export type GroupKey =
  | "pinned"
  | "recent"
  | "context"
  | "actions"
  | "servers"
  | "nodes"
  | "pages"
  | "eggs"
  | "members"
  | "docs";

export const groupNames: Record<GroupKey, string> = {
  pinned: "Pinned",
  recent: "Recent",
  context: "This page",
  actions: "Actions",
  servers: "Servers",
  nodes: "Nodes",
  pages: "Pages",
  eggs: "Eggs",
  members: "Members",
  docs: "Docs",
};

// Icons by name; the menu maps them to lucide icons.
export type IconKey =
  | "page"
  | "server"
  | "node"
  | "egg"
  | "member"
  | "docs"
  | "help"
  | "plus"
  | "invite"
  | "org"
  | "staff"
  | "account"
  | "tab"
  | ServerAct;

export type CommandItem = {
  // Stable, for recents and pins: "server:<id>", "act:restart:<id>"…
  id: string;
  label: string;
  // A second, quieter line: the server's egg and node, the tab's server.
  hint?: string;
  group: GroupKey;
  keywords?: string[];
  icon: IconKey;
  run: CommandRun;
  // Tab opens these: a server's, node's, or egg's commands.
  drill?: CommandRun;
  // Extra rank: the page you're on.
  boost?: number;
  // Keys shown beside it ("Mod+K").
  shortcut?: string;
  // Hidden until something is typed: per-server actions and tabs for
  // servers other than the one you're on, which would flood an empty menu.
  searchOnly?: boolean;
  destructive?: boolean;
};

// The facts about a server the menu needs (from its ListServers entry).
export type ServerInfo = {
  id: string;
  name: string;
  nodeId: string;
  nodeName: string;
  nodeConnected: boolean;
  egg: string;
  // Where players connect: its shown address, or the node's hostname and
  // port (copying it doesn't show it).
  address: string;
  state: string;
  installState: string;
  status: string;
  permissions: readonly string[];
  // A Minecraft Java server's player lists (#422), and RCON games' (#423).
  playerLists: boolean;
  rconPlayers: string;
};

export type NodeInfo = { id: string; name: string; hostname: string; connected: boolean };
export type OrgInfo = { id: string; name: string; admin: boolean };
export type EggInfo = { id: string; name: string; category: string; categoryName: string };
export type MemberInfo = { userId: string; email: string; name: string; role: string };

// can is lib/org-data's can(), for a ServerInfo.
export function can(s: Pick<ServerInfo, "permissions"> | undefined, p: string): boolean {
  return !!s && (s.permissions.includes("*") || s.permissions.includes(p));
}

// What can be done to a server from the menu.
export type ServerAct =
  | "start"
  | "restart"
  | "stop"
  | "kill"
  | "command"
  | "backup"
  | "schedule"
  | "addons"
  | "whitelist"
  | "op"
  | "kick"
  | "files"
  | "console"
  | "address"
  | "dashboard"
  | "share-log"
  | "test-connection";

// What a server's page knows that its list entry doesn't (from its
// config): which tabs the egg gives it, and its web dashboard. Only the
// server you're on has it; for others the menu assumes the best and the
// action says so if it can't.
export type ServerExtras = {
  game: boolean;
  addons: boolean;
  // An add-on kind with an installer (plugins, mods): something to update.
  addonKinds: boolean;
  players: boolean;
  worlds: boolean;
  dashboard?: { name: string; url: string };
};

// An action's input step.
export type ActInput = {
  label: string;
  placeholder: string;
  // What the item says for a value ("Send “say hi” to Survival").
  submit: (value: string, server: string) => string;
  // Why a value won't do, or undefined when it will.
  check: (value: string) => string | undefined;
};

export type ActConfirm = {
  title: (server: string) => string;
  description: string;
  confirm: string;
  destructive: boolean;
};

type ActDef = {
  act: ServerAct;
  // For a server: "Restart Survival".
  label: (server: string) => string;
  // Without a server, to pick one: "Restart a server…".
  verb: string;
  // In a server's own list: "Restart".
  short: string;
  keywords: string[];
  allowed: (s: ServerInfo, x?: ServerExtras) => boolean;
  input?: ActInput;
  confirm?: ActConfirm;
};

const running = (s: ServerInfo) => s.state === "running" || s.state === "starting";
const installing = (s: ServerInfo) =>
  ["pending", "installing", "failed"].includes(s.installState) ||
  s.state === "installing" ||
  s.state === "install_failed";
// Power buttons act on these states; anything else (moving, restoring…)
// waits until it's done, as on the server's page. Unknown states allow.
const powerReady = (s: ServerInfo) =>
  s.nodeConnected &&
  !installing(s) &&
  (s.state === "" || ["running", "starting", "offline", "crashed"].includes(s.state));
const minecraft = (s: ServerInfo) => s.playerLists && !s.rconPlayers;
// The Players tab's own rule for changing lists: the players permission,
// or console commands while it runs, or its files while it's stopped.
const canChangePlayers = (s: ServerInfo) =>
  can(s, "players") || (running(s) ? can(s, "console.write") : can(s, "files.write"));

const playerInput = (what: string): ActInput => ({
  label: "Player name",
  placeholder: "A Minecraft name, like Steve",
  submit: (v, server) => `${what} ${v || "…"} on ${server}`,
  check: (v) =>
    !v
      ? "Type a player's name."
      : validName(v)
        ? undefined
        : "Names are 1–16 letters, digits, or _.",
});

export const serverActs: ActDef[] = [
  {
    act: "start",
    label: (n) => `Start ${n}`,
    verb: "Start a server…",
    short: "Start",
    keywords: ["power", "boot", "run", "on"],
    allowed: (s) => can(s, "power") && powerReady(s) && !running(s),
  },
  {
    act: "restart",
    label: (n) => `Restart ${n}`,
    verb: "Restart a server…",
    short: "Restart",
    keywords: ["power", "reboot", "reload"],
    allowed: (s) => can(s, "power") && powerReady(s),
  },
  {
    act: "stop",
    label: (n) => `Stop ${n}`,
    verb: "Stop a server…",
    short: "Stop",
    keywords: ["power", "shutdown", "off", "halt"],
    allowed: (s) =>
      can(s, "power") && powerReady(s) && s.state !== "offline" && s.state !== "crashed",
    confirm: {
      title: (n) => `Stop ${n}?`,
      description: "Players on it are disconnected. It saves and shuts down the way the game does.",
      confirm: "Stop",
      destructive: false,
    },
  },
  {
    act: "kill",
    label: (n) => `Kill ${n}`,
    verb: "Kill a server…",
    short: "Kill",
    keywords: ["power", "force", "stop", "terminate"],
    // Also while it's stuck stopping: that's when it's needed.
    allowed: (s) =>
      can(s, "power") &&
      s.nodeConnected &&
      !installing(s) &&
      ["running", "starting", "stopping"].includes(s.state),
    confirm: {
      title: (n) => `Kill ${n}?`,
      description:
        "It's stopped at once, without saving: anything since its last save is lost. Use it when Stop doesn't work.",
      confirm: "Kill",
      destructive: true,
    },
  },
  {
    act: "command",
    label: (n) => `Send a command to ${n}`,
    verb: "Send a console command…",
    short: "Send a command",
    keywords: ["console", "run", "execute", "say", "rcon"],
    allowed: (s) => can(s, "console.write") && s.nodeConnected && running(s),
    input: {
      label: "Command",
      placeholder: "A console command, like say Hello",
      submit: (v, server) => (v ? `Send “${v}” to ${server}` : `Type a command for ${server}`),
      check: (v) => (v.trim() ? undefined : "Type a command."),
    },
  },
  {
    act: "console",
    label: (n) => `Open ${n}'s console`,
    verb: "Open a server's console…",
    short: "Console",
    keywords: ["terminal", "log", "output"],
    allowed: (s) => can(s, "console.read") || can(s, "console.write"),
  },
  {
    act: "backup",
    label: (n) => `Back up ${n}`,
    verb: "Back up a server…",
    short: "Back up now",
    keywords: ["backup", "snapshot", "save", "create"],
    allowed: (s) => can(s, "backups") && s.nodeConnected,
  },
  {
    act: "schedule",
    label: (n) => `Run a schedule on ${n}`,
    verb: "Run a schedule…",
    short: "Run a schedule",
    keywords: ["schedule", "task", "cron", "now"],
    allowed: (s) => can(s, "schedules") && s.nodeConnected,
  },
  {
    act: "addons",
    label: (n) => `Update all add-ons on ${n}`,
    verb: "Update all add-ons…",
    short: "Update all add-ons",
    keywords: ["plugins", "mods", "datapacks", "upgrade", "outdated"],
    allowed: (s, x) => can(s, "files.write") && s.nodeConnected && (x ? x.addonKinds : true),
  },
  {
    act: "whitelist",
    label: (n) => `Whitelist a player on ${n}`,
    verb: "Whitelist a player…",
    short: "Whitelist a player",
    keywords: ["allowlist", "players", "add", "minecraft"],
    allowed: (s) => minecraft(s) && canChangePlayers(s),
    input: playerInput("Whitelist"),
  },
  {
    act: "op",
    label: (n) => `Op a player on ${n}`,
    verb: "Op a player…",
    short: "Op a player",
    keywords: ["operator", "admin", "players", "minecraft"],
    allowed: (s) => minecraft(s) && canChangePlayers(s),
    input: playerInput("Op"),
  },
  {
    act: "kick",
    label: (n) => `Kick a player from ${n}`,
    verb: "Kick a player…",
    short: "Kick a player",
    keywords: ["remove", "disconnect", "players", "minecraft"],
    allowed: (s) => minecraft(s) && running(s) && (can(s, "players") || can(s, "console.write")),
    input: playerInput("Kick"),
  },
  {
    act: "files",
    label: (n) => `Open ${n}'s files at…`,
    verb: "Open a server's files at…",
    short: "Open files at…",
    keywords: ["file manager", "folder", "path", "browse", "directory"],
    allowed: (s) => can(s, "files.read"),
    input: {
      label: "Path",
      placeholder: "A folder or file, like plugins or logs/latest.log",
      submit: (v, server) =>
        v ? `Open ${server}'s files at /${cleanPath(v)}` : `Open ${server}'s files`,
      check: () => undefined,
    },
  },
  {
    act: "address",
    label: (n) => `Copy ${n}'s address`,
    verb: "Copy a server's address…",
    short: "Copy the address",
    keywords: ["ip", "connect", "join", "clipboard", "host", "port"],
    allowed: (s) => !!s.address,
  },
  {
    act: "dashboard",
    label: (n) => `Open ${n}'s web dashboard`,
    verb: "Open a server's web dashboard…",
    short: "Open the web dashboard",
    keywords: ["web", "panel", "txadmin", "dashboard", "admin"],
    // Only the server you're on knows its dashboard.
    allowed: (_s, x) => !!x?.dashboard,
  },
  {
    act: "share-log",
    label: (n) => `Share ${n}'s log`,
    verb: "Share a server's log…",
    short: "Share the log (mclo.gs)",
    keywords: ["mclogs", "mclo.gs", "console", "paste", "support", "upload"],
    allowed: (s) => can(s, "console.read") || can(s, "console.write"),
  },
  {
    act: "test-connection",
    label: (n) => `Test ${n}'s connection`,
    verb: "Test a server's connection…",
    short: "Test the connection",
    keywords: ["ports", "firewall", "reach", "join", "network", "cant connect"],
    allowed: () => true,
  },
];

export function actDef(act: ServerAct): ActDef {
  return serverActs.find((d) => d.act === act) as ActDef;
}

// cleanPath is a typed path as the file manager takes it: no leading or
// trailing slashes, no empty or "." parts.
export function cleanPath(p: string): string {
  return p
    .trim()
    .split("/")
    .filter((x) => x && x !== ".")
    .join("/");
}

// filesSearch is where Files opens for a typed path: a path with a dot in
// its last part is taken as a file (opened in its folder), else a folder.
export function filesSearch(p: string): Record<string, string> {
  const path = cleanPath(p);
  if (!path) return {};
  const parts = path.split("/");
  const last = parts.at(-1) ?? "";
  if (/\.[^.]+$/.test(last) && !last.startsWith(".")) {
    const dir = parts.slice(0, -1).join("/");
    return dir ? { path: dir, file: path } : { file: path };
  }
  return { path };
}

const serverBase = "/orgs/$orgId/servers/$serverId";
const tabRoutes: Record<ServerTabKey, string> = {
  overview: serverBase,
  game: `${serverBase}/game`,
  addons: `${serverBase}/addons`,
  players: `${serverBase}/players`,
  worlds: `${serverBase}/worlds`,
  files: `${serverBase}/files`,
  backups: `${serverBase}/backups`,
  schedules: `${serverBase}/schedules`,
  databases: `${serverBase}/databases`,
  network: `${serverBase}/network`,
  startup: `${serverBase}/startup`,
  settings: `${serverBase}/settings`,
  activity: `${serverBase}/activity`,
  access: `${serverBase}/access`,
};

export function tabRoute(key: ServerTabKey): string {
  return tabRoutes[key];
}

// tabsOf is the server's tabs, as its page shows them: from what the
// server's page knows (extras), or, for another server, the tabs that
// don't depend on its egg's config.
export function tabsOf(s: ServerInfo, x?: ServerExtras): ServerTab[] {
  const t = serverTabs({
    can: (p) => can(s, p),
    game: x?.game ?? false,
    addons: x?.addons ?? false,
    // The Players tab needs nothing from the config (lib/players-tab).
    players:
      x?.players ??
      (s.rconPlayers
        ? can(s, "players")
        : s.playerLists && (can(s, "players") || can(s, "files.read") || can(s, "console.write"))),
    worlds: x?.worlds ?? false,
  });
  return [...t.bar, ...t.more];
}

const tabKeywords: Partial<Record<ServerTabKey, string[]>> = {
  overview: ["console", "stats", "home"],
  game: ["version", "properties", "settings", "motd"],
  addons: ["plugins", "mods", "workshop", "datapacks"],
  players: ["whitelist", "ops", "bans"],
  files: ["file manager", "sftp", "folder"],
  backups: ["restore", "snapshot"],
  schedules: ["cron", "tasks", "restart"],
  network: ["ports", "address", "connection", "domain"],
  startup: ["variables", "image", "command", "java"],
  settings: ["rename", "reinstall", "delete", "move", "memory"],
  activity: ["audit", "log", "history"],
  access: ["permissions", "members", "sharing", "users"],
};

// serverItems is a server's own commands: its tabs and what the user can
// do to it. label "own" (in the server's own list, or on its page) drops
// the server's name from the labels.
export function serverItems(
  orgId: string,
  s: ServerInfo,
  opts: {
    extras?: ServerExtras;
    own?: boolean;
    group?: GroupKey;
    boost?: number;
    searchOnly?: boolean;
  } = {},
): CommandItem[] {
  const { extras, own = false, group = "actions", boost, searchOnly } = opts;
  const params = { orgId, serverId: s.id };
  const out: CommandItem[] = [];
  const name = s.name;
  for (const d of serverActs) {
    if (!d.allowed(s, extras)) continue;
    const base = {
      id: `act:${d.act}:${s.id}`,
      label: own ? d.short : d.label(name),
      // In the page's own group its heading names the server already.
      hint:
        own || group === "context" ? undefined : `${s.egg}${s.nodeName ? ` · ${s.nodeName}` : ""}`,
      group,
      keywords: [...d.keywords, name, d.verb],
      icon: d.act,
      boost,
      searchOnly,
      destructive: d.confirm?.destructive,
    } satisfies Partial<CommandItem>;
    if (d.act === "console") {
      out.push({ ...base, run: { kind: "go", to: serverBase, params } });
    } else if (d.act === "dashboard" && extras?.dashboard) {
      out.push({
        ...base,
        hint: extras.dashboard.name,
        run: { kind: "open", url: extras.dashboard.url },
      });
    } else {
      out.push({ ...base, run: { kind: "act", act: d.act, serverId: s.id } });
    }
  }
  for (const t of tabsOf(s, extras)) {
    out.push({
      id: `tab:${t.key}:${s.id}`,
      label: own ? t.label : `${name} › ${t.label}`,
      group,
      keywords: [...(tabKeywords[t.key] ?? []), name, "tab"],
      icon: "tab",
      run: { kind: "go", to: tabRoutes[t.key], params },
      boost: boost !== undefined ? boost - 1 : undefined,
      searchOnly,
    });
  }
  return out;
}

// serverItem is a server itself: Enter opens it, Tab lists its commands.
export function serverItem(orgId: string, s: ServerInfo, boost?: number): CommandItem {
  return {
    id: `server:${s.id}`,
    label: s.name,
    hint: [s.status, s.egg, s.nodeName].filter(Boolean).join(" · "),
    group: "servers",
    keywords: [s.egg, s.nodeName, s.address, "server"].filter(Boolean),
    icon: "server",
    run: { kind: "go", to: serverBase, params: { orgId, serverId: s.id } },
    drill: { kind: "server", serverId: s.id },
    boost,
  };
}

// pickItems is the "Restart a server…" kind: an act to pick a server for,
// offered when at least one server allows it.
export function pickItems(servers: readonly ServerInfo[]): CommandItem[] {
  return serverActs
    .filter((d) => d.act !== "dashboard" && servers.some((s) => d.allowed(s)))
    .map((d) => ({
      id: `pick:${d.act}`,
      label: d.verb,
      group: "actions" as const,
      keywords: d.keywords,
      icon: d.act,
      run: { kind: "pick" as const, act: d.act },
      destructive: d.confirm?.destructive,
    }));
}

// pickable is the servers act can be done to, for its pick step.
export function pickable(act: ServerAct, servers: readonly ServerInfo[]): ServerInfo[] {
  const d = actDef(act);
  return servers.filter((s) => d.allowed(s));
}

const nodeBase = "/orgs/$orgId/nodes/$nodeId";
const nodeTabs: { key: string; label: string; admin: boolean; keywords: string[] }[] = [
  { key: "", label: "Overview", admin: false, keywords: ["stats", "servers"] },
  { key: "health", label: "Health", admin: true, keywords: ["status", "problems", "uptime"] },
  { key: "backups", label: "Backups", admin: true, keywords: ["storage", "destinations"] },
  { key: "disks", label: "Disks", admin: true, keywords: ["storage", "volumes", "space"] },
  { key: "databases", label: "Databases", admin: true, keywords: ["mysql", "postgres", "redis"] },
  { key: "networks", label: "Networks", admin: true, keywords: ["firewall", "wireguard"] },
  { key: "system", label: "System", admin: true, keywords: ["updates", "reboot", "os"] },
  { key: "activity", label: "Activity", admin: true, keywords: ["audit", "log"] },
  { key: "settings", label: "Settings", admin: true, keywords: ["rename", "hostname", "remove"] },
];

// nodeItems is a node's own commands: its tabs (the ones its page shows
// this user), deploying a server to it, and copying its hostname.
export function nodeItems(
  org: OrgInfo,
  n: NodeInfo,
  opts: { own?: boolean; group?: GroupKey; boost?: number; searchOnly?: boolean } = {},
): CommandItem[] {
  const { own = false, group = "actions", boost, searchOnly } = opts;
  const params = { orgId: org.id, nodeId: n.id };
  const out: CommandItem[] = [];
  for (const t of nodeTabs) {
    if (t.admin && !org.admin) continue;
    out.push({
      id: `ntab:${t.key || "overview"}:${n.id}`,
      label: own ? t.label : `${n.name} › ${t.label}`,
      group,
      keywords: [...t.keywords, n.name, "node", "tab"],
      icon: "tab",
      run: { kind: "go", to: t.key ? `${nodeBase}/${t.key}` : nodeBase, params },
      boost: boost !== undefined ? boost - 1 : undefined,
      searchOnly,
    });
  }
  if (org.admin) {
    out.push({
      id: `deploy:${n.id}`,
      label: own ? "Deploy a server here" : `Deploy a server on ${n.name}`,
      group,
      keywords: ["create", "new", "server", n.name],
      icon: "plus",
      run: {
        kind: "go",
        to: "/orgs/$orgId/servers/new",
        params: { orgId: org.id },
        search: { node: n.id },
      },
      boost,
      searchOnly,
    });
  }
  if (n.hostname) {
    out.push({
      id: `nhost:${n.id}`,
      label: own ? "Copy the hostname" : `Copy ${n.name}'s hostname`,
      group,
      keywords: ["address", "ip", "clipboard", n.name],
      icon: "address",
      run: { kind: "copy", text: n.hostname, what: "hostname" },
      boost,
      searchOnly,
    });
  }
  return out;
}

export function nodeItem(orgId: string, n: NodeInfo, boost?: number): CommandItem {
  return {
    id: `node:${n.id}`,
    label: n.name,
    hint: n.connected ? "Connected" : "Offline",
    group: "nodes",
    keywords: ["node", "machine"],
    icon: "node",
    run: { kind: "go", to: nodeBase, params: { orgId, nodeId: n.id } },
    drill: { kind: "node", nodeId: n.id },
    boost,
  };
}

export function eggItem(orgId: string, e: EggInfo): CommandItem {
  return {
    id: `egg:${e.id}`,
    label: e.name,
    hint: e.categoryName,
    group: "eggs",
    keywords: [e.categoryName, e.category, "egg", "game"],
    icon: "egg",
    run: { kind: "go", to: "/orgs/$orgId/eggs/$eggId", params: { orgId, eggId: e.id } },
    drill: { kind: "egg", eggId: e.id },
  };
}

// eggItems is an egg's own commands: open it, and make a server from it.
export function eggItems(org: OrgInfo, e: EggInfo): CommandItem[] {
  const out: CommandItem[] = [
    {
      id: `egg:${e.id}`,
      label: "Open in the library",
      group: "actions",
      icon: "egg",
      run: { kind: "go", to: "/orgs/$orgId/eggs/$eggId", params: { orgId: org.id, eggId: e.id } },
    },
  ];
  if (org.admin) {
    out.push({
      id: `eggnew:${e.id}`,
      label: `Create a ${e.name} server`,
      group: "actions",
      keywords: ["deploy", "new"],
      icon: "plus",
      run: {
        kind: "go",
        to: "/orgs/$orgId/servers/new",
        params: { orgId: org.id },
        search: { egg: e.id },
      },
    });
  }
  return out;
}

export function memberItem(orgId: string, m: MemberInfo): CommandItem {
  return {
    id: `member:${m.userId}`,
    label: m.name || m.email,
    hint: m.name ? `${m.email} · ${m.role}` : m.role,
    group: "members",
    keywords: [m.email, m.role, "member", "user"],
    icon: "member",
    run: { kind: "go", to: "/orgs/$orgId/settings/members", params: { orgId } },
  };
}

type PageDef = {
  key: string;
  label: string;
  to: string;
  keywords: string[];
  admin?: boolean;
  icon?: IconKey;
};

const orgPages: PageDef[] = [
  {
    key: "overview",
    label: "Overview",
    to: "/orgs/$orgId",
    keywords: ["home", "dashboard", "org"],
  },
  { key: "servers", label: "Servers", to: "/orgs/$orgId/servers", keywords: ["games", "list"] },
  { key: "nodes", label: "Nodes", to: "/orgs/$orgId/nodes", keywords: ["machines", "hosts"] },
  {
    key: "databases",
    label: "Databases",
    to: "/orgs/$orgId/databases",
    keywords: ["mysql", "postgres", "redis", "mongodb"],
    admin: true,
  },
  {
    key: "eggs",
    label: "Egg library",
    to: "/orgs/$orgId/eggs",
    keywords: ["eggs", "games", "templates"],
  },
  {
    key: "settings",
    label: "Org settings",
    to: "/orgs/$orgId/settings",
    keywords: ["general", "rename", "logo"],
  },
  {
    key: "members",
    label: "Members",
    to: "/orgs/$orgId/settings/members",
    keywords: ["team", "users", "invite", "roles"],
  },
  {
    key: "billing",
    label: "Billing",
    to: "/orgs/$orgId/settings/billing",
    keywords: ["plan", "invoices", "card", "payment"],
  },
  {
    key: "backup-settings",
    label: "Backup storage",
    to: "/orgs/$orgId/settings/backups",
    keywords: ["backups", "destinations", "s3", "b2"],
    admin: true,
  },
  {
    key: "alerts",
    label: "Alerts",
    to: "/orgs/$orgId/settings/alerts",
    keywords: ["notifications", "discord", "email", "webhook"],
    admin: true,
  },
  {
    key: "audit",
    label: "Audit log",
    to: "/orgs/$orgId/settings/activity",
    keywords: ["activity", "history"],
    admin: true,
  },
  {
    key: "new-node",
    label: "Add a node",
    to: "/orgs/$orgId/nodes/new",
    keywords: ["connect", "machine", "install"],
    admin: true,
    icon: "plus",
  },
];

const accountPages: PageDef[] = [
  {
    key: "account",
    label: "Account settings",
    to: "/settings",
    keywords: ["profile", "name", "email", "theme", "avatar"],
  },
  {
    key: "security",
    label: "Security",
    to: "/settings/security",
    keywords: ["passkeys", "password", "2fa", "sessions", "authenticator"],
  },
  {
    key: "account-activity",
    label: "Account activity",
    to: "/settings/activity",
    keywords: ["sign ins", "history"],
  },
];

const staffPages: PageDef[] = [
  { key: "admin", label: "Raptor admin", to: "/admin", keywords: ["staff", "overview"] },
  {
    key: "admin-users",
    label: "Users (staff)",
    to: "/admin/users",
    keywords: ["staff", "accounts"],
  },
  {
    key: "admin-orgs",
    label: "Orgs (staff)",
    to: "/admin/orgs",
    keywords: ["staff", "organizations"],
  },
  {
    key: "admin-support",
    label: "Support requests (staff)",
    to: "/admin/support",
    keywords: ["staff", "tickets"],
  },
  {
    key: "admin-eggs",
    label: "Official eggs (staff)",
    to: "/admin/eggs",
    keywords: ["staff", "library"],
  },
  {
    key: "admin-announcements",
    label: "Announcements (staff)",
    to: "/admin/announcements",
    keywords: ["staff", "banner"],
  },
  {
    key: "admin-settings",
    label: "Raptor settings (staff)",
    to: "/admin/settings",
    keywords: ["staff"],
  },
  {
    key: "admin-infra",
    label: "Infrastructure (staff)",
    to: "/admin/overview/infrastructure",
    keywords: ["staff", "health"],
  },
];

// pageItems is every page the user can open: the org's (admin-only ones
// for admins), their account's, staff tools for staff, other orgs, and
// help.
export function pageItems(opts: {
  org?: OrgInfo;
  orgs: readonly OrgInfo[];
  staff: boolean;
  docsUrl: string;
  discordUrl: string;
}): CommandItem[] {
  const { org, orgs, staff } = opts;
  const out: CommandItem[] = [];
  const page = (p: PageDef, params?: Record<string, string>): CommandItem => ({
    id: `page:${p.key}`,
    label: p.label,
    group: "pages",
    keywords: p.keywords,
    icon: p.icon ?? "page",
    run: { kind: "go", to: p.to, params },
  });
  if (org) {
    for (const p of orgPages) if (!p.admin || org.admin) out.push(page(p, { orgId: org.id }));
  }
  for (const p of accountPages) out.push({ ...page(p), icon: "account" });
  if (staff) for (const p of staffPages) out.push({ ...page(p), icon: "staff" });
  for (const o of orgs) {
    if (o.id === org?.id) continue;
    out.push({
      id: `org:${o.id}`,
      label: `Switch to ${o.name}`,
      group: "pages",
      keywords: ["org", "organization", "switch", o.name],
      icon: "org",
      run: { kind: "go", to: "/orgs/$orgId", params: { orgId: o.id } },
    });
  }
  out.push(
    {
      id: "help:shortcuts",
      label: "Keyboard shortcuts",
      group: "pages",
      keywords: ["keys", "help", "hotkeys"],
      icon: "help",
      run: { kind: "help" },
      shortcut: "?",
    },
    {
      id: "help:docs",
      label: "Open the docs",
      group: "pages",
      keywords: ["help", "guide", "documentation"],
      icon: "docs",
      run: { kind: "open", url: opts.docsUrl },
    },
    {
      id: "help:discord",
      label: "Get help on Discord",
      group: "pages",
      keywords: ["support", "community", "chat"],
      icon: "docs",
      run: { kind: "open", url: opts.discordUrl },
    },
  );
  return out;
}

// orgActions is what an admin can start in the org: a new server, an
// invitation.
export function orgActions(org: OrgInfo | undefined): CommandItem[] {
  if (!org?.admin) return [];
  return [
    {
      id: "new-server",
      label: "Create a server",
      group: "actions",
      keywords: ["new", "deploy", "add", "server"],
      icon: "plus",
      run: { kind: "go", to: "/orgs/$orgId/servers/new", params: { orgId: org.id } },
    },
    {
      id: "invite",
      label: "Invite a member",
      group: "actions",
      keywords: ["add", "team", "user", "email", "member"],
      icon: "invite",
      run: { kind: "invite" },
    },
  ];
}

// docsItem searches the docs site for the query.
export function docsItem(docsUrl: string, query: string): CommandItem {
  const q = query.trim();
  return {
    id: "docs:search",
    label: `Search the docs for “${q}”`,
    group: "docs",
    icon: "docs",
    run: { kind: "open", url: `${docsUrl}/?q=${encodeURIComponent(q)}` },
  };
}

// inviteCheck is why an email won't do for an invitation, or undefined.
export function inviteCheck(email: string): string | undefined {
  const e = email.trim();
  if (!e) return "Type their email address.";
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(e) ? undefined : "That isn't an email address.";
}

// contextOf is what a page is about, from its path: a server or a node
// (by the short or full id in the URL), or nothing.
export function contextOf(
  pathname: string,
): { kind: "server"; ref: string } | { kind: "node"; ref: string } | undefined {
  const m = /^\/orgs\/[^/]+\/(servers|nodes)\/([^/]+)/.exec(pathname);
  if (!m) return undefined;
  const ref = m[2] ?? "";
  if (!ref || ref === "new") return undefined;
  return { kind: m[1] === "servers" ? "server" : "node", ref };
}
