// A server's tabs (#436): which ones a user sees, in what order, and which
// sit in the tab bar or under More. Pure, so it is tested in node.
//
// The bar is what a beginner needs most: Overview, then the game's own
// tabs when the egg has them (Game, Add-ons, Players, Worlds), then Files,
// Backups, Schedules. The rest, used less often or by power users, go
// under More: Databases, Network, Startup, Settings, Activity, Access.

export type ServerTabKey =
  | "overview"
  | "game"
  | "addons"
  | "players"
  | "worlds"
  | "files"
  | "backups"
  | "schedules"
  | "databases"
  | "network"
  | "startup"
  | "settings"
  | "activity"
  | "access";

export type ServerTabGroup = "bar" | "more";

// path is the segment after the server's id ("" for Overview).
export type ServerTab = {
  key: ServerTabKey;
  label: string;
  path: string;
  group: ServerTabGroup;
};

// What decides the tabs: the user's permissions on the server, and the
// game tabs the egg declares something for. hidden drops tabs by key, for
// rules on top (the public demo may hide some, #449).
export type ServerTabInputs = {
  can: (permission: string) => boolean;
  game: boolean;
  addons: boolean;
  players: boolean;
  worlds: boolean;
  hidden?: ReadonlySet<ServerTabKey>;
};

type Def = ServerTab & { show: (i: ServerTabInputs) => boolean };

// In order: the bar's tabs, then More's.
const defs: Def[] = [
  { key: "overview", label: "Overview", path: "", group: "bar", show: () => true },
  { key: "game", label: "Game", path: "game", group: "bar", show: (i) => i.game },
  { key: "addons", label: "Add-ons", path: "addons", group: "bar", show: (i) => i.addons },
  { key: "players", label: "Players", path: "players", group: "bar", show: (i) => i.players },
  { key: "worlds", label: "Worlds", path: "worlds", group: "bar", show: (i) => i.worlds },
  { key: "files", label: "Files", path: "files", group: "bar", show: (i) => i.can("files.read") },
  {
    key: "backups",
    label: "Backups",
    path: "backups",
    group: "bar",
    show: (i) => i.can("backups"),
  },
  {
    key: "schedules",
    label: "Schedules",
    path: "schedules",
    group: "bar",
    show: (i) => i.can("schedules"),
  },
  {
    key: "databases",
    label: "Databases",
    path: "databases",
    group: "more",
    show: (i) => i.can("databases.read") || i.can("databases.write"),
  },
  { key: "network", label: "Network", path: "network", group: "more", show: () => true },
  {
    key: "startup",
    label: "Startup",
    path: "startup",
    group: "more",
    show: (i) => i.can("startup"),
  },
  {
    key: "settings",
    label: "Settings",
    path: "settings",
    group: "more",
    show: (i) => i.can("startup") || i.can("reinstall"),
  },
  { key: "activity", label: "Activity", path: "activity", group: "more", show: (i) => i.can("*") },
  { key: "access", label: "Access", path: "access", group: "more", show: (i) => i.can("*") },
];

// serverTabs is the tabs this user sees on a server, split into the bar
// and More, each in order.
export function serverTabs(i: ServerTabInputs): { bar: ServerTab[]; more: ServerTab[] } {
  const bar: ServerTab[] = [];
  const more: ServerTab[] = [];
  for (const d of defs) {
    if (i.hidden?.has(d.key) || !d.show(i)) continue;
    const tab = { key: d.key, label: d.label, path: d.path, group: d.group };
    (d.group === "bar" ? bar : more).push(tab);
  }
  // One tab alone under More is just a hidden tab: put it in the bar.
  const [lone] = more;
  if (lone && more.length === 1) return { bar: [...bar, { ...lone, group: "bar" }], more: [] };
  return { bar, more };
}

// Old paths that now live in another tab.
const moved: Record<string, ServerTabKey> = { workshop: "addons" };

// activeServerTab is the tab a URL is in: the segment after
// /servers/<id>/ ("" for Overview); undefined off a server's pages.
export function activeServerTab(pathname: string): ServerTabKey | undefined {
  const parts = pathname.split("/").filter(Boolean);
  const at = parts.indexOf("servers");
  if (at < 0 || parts.length < at + 2) return undefined;
  const seg = parts[at + 2] ?? "";
  if (seg === "") return "overview";
  if (moved[seg]) return moved[seg];
  return defs.find((d) => d.path === seg)?.key;
}
