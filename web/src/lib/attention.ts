// What a server's "Needs attention" card says (#435, docs/PANEL.md#needs-
// attention): each thing an owner should act on, how bad it is, the one
// action that fixes it, and, when the viewer can't, who can. The card's
// components fetch the data; this decides. Kept free of imports so
// `node --test` can check it.

// Rank orders the card, worst first: the server can't start, then
// players can't join, then security, then what may break, then updates.
export const rank = {
  broken: 0,
  players: 1,
  security: 2,
  risk: 3,
  update: 4,
} as const;

// Where an item's button goes, on this server.
export type Target = "settings" | "files" | "addons";

export type AttentionItem = {
  // Stable for the thing it's about, so a dismissal sticks to it.
  id: string;
  // Changes when what it says changes, which brings a dismissed item back.
  sig: string;
  rank: number;
  // What's wrong, in one sentence.
  text: string;
  // The one button, for viewers who may do it.
  action?: { label: string; to: Target; kind?: string };
  // Instead of the button, for viewers who can't: who can.
  blocked?: string;
};

// The permissions a fix needs, in the words the Access page uses.
const permissionNames: Record<string, string> = {
  "files.write": "Change files",
  reinstall: "Reinstall",
};

// whoCan says who can do what the viewer can't.
export function whoCan(permission: string, what: string): string {
  if (permission === "*") return `An org admin or owner can ${what}.`;
  return `Someone with the "${permissionNames[permission] ?? permission}" permission can ${what}.`;
}

// installItem: an install that failed leaves nothing to start.
export function installItem(
  s: { installState: string; installError: string; state?: string },
  canReinstall: boolean,
): AttentionItem | undefined {
  if (s.installState !== "failed" && s.state !== "install_failed") return undefined;
  const why = s.installError.trim();
  return {
    id: "install",
    sig: why || "failed",
    rank: rank.broken,
    text: `The install failed, so the server can't start${why ? `: ${why.replace(/[.!]$/, "")}` : ""}. Fix the cause, then reinstall it.`,
    ...(canReinstall
      ? { action: { label: "Reinstall", to: "settings" as const } }
      : { blocked: whoCan("reinstall", "reinstall it from Settings") }),
  };
}

// diskWarn is how full a disk with a limit has to be to show.
export const diskWarn = 0.9;

// diskItem: a disk near its limit, from the Overview's live stats (no
// query of its own). The signature is the step (90, 95, 99%), so a
// dismissal lasts until it gets worse.
export function diskItem(
  used: number,
  limit: number,
  canWriteFiles: boolean,
  size: (n: number) => string = String,
): AttentionItem | undefined {
  if (!(limit > 0) || !(used >= 0)) return undefined;
  const f = used / limit;
  if (f < diskWarn) return undefined;
  const step = f >= 0.99 ? 99 : f >= 0.95 ? 95 : 90;
  return {
    id: "disk",
    sig: String(step),
    rank: f >= 0.99 ? rank.broken : rank.risk,
    text: `The disk is ${Math.min(100, Math.floor(f * 100))}% full (${size(used)} of ${size(limit)}). When it fills, the server can't save; delete old files or ask for more space.`,
    ...(canWriteFiles
      ? { action: { label: "Open Files", to: "files" as const } }
      : { blocked: whoCan("files.write", "free up space") }),
  };
}

// Addon is what the card needs of an identified add-on (IdentifyAddons).
export type Addon = {
  sha512: string;
  title: string;
  projectId: string;
  // The newer version's ID, when there's one for this server.
  latestId?: string;
  required: string[];
};

// addonItems: for one add-on kind, those missing one they need and those
// with an update. handAdded: some files nobody knows, which may be the
// missing one, put there by hand; then the missing row stays quiet, as
// the Add-ons tab only says "May need".
export function addonItems(
  kind: string,
  noun: string,
  addons: Addon[],
  handAdded: boolean,
  canWriteFiles: boolean,
): AttentionItem[] {
  const out: AttentionItem[] = [];
  const installed = new Set(addons.map((a) => a.projectId));
  const nouns = (n: number) => `${noun}${n === 1 ? "" : "s"}`;
  const missing = addons.filter((a) => a.required.some((p) => !installed.has(p)));
  if (missing.length > 0 && !handAdded) {
    out.push({
      id: `missing:${kind}`,
      sig: missing.map((a) => a.sha512).join(","),
      rank: rank.risk,
      text:
        missing.length === 1
          ? `${missing[0]?.title} is missing another ${noun} it needs, so it may not load.`
          : `${missing.length} ${nouns(missing.length)} are missing others they need, so they may not load.`,
      ...(canWriteFiles
        ? { action: { label: "Install what's missing", to: "addons" as const, kind } }
        : { blocked: whoCan("files.write", "install them") }),
    });
  }
  const outdated = addons.filter((a) => a.latestId);
  if (outdated.length > 0) {
    out.push({
      id: `updates:${kind}`,
      sig: outdated.map((a) => a.latestId).join(","),
      rank: rank.update,
      text:
        outdated.length === 1
          ? `${outdated[0]?.title} has an update.`
          : `${outdated.length} ${nouns(outdated.length)} have an update.`,
      ...(canWriteFiles
        ? { action: { label: "Review updates", to: "addons" as const, kind } }
        : { blocked: whoCan("files.write", "update them") }),
    });
  }
  return out;
}

// eggItem: the server's library egg has a newer version. Only admins and
// owners change a server's egg, so others don't see it at all (they
// can't act, and it isn't broken).
export function eggItem(v: {
  name: string;
  latest: number;
  current?: number;
  outdated: boolean;
}): AttentionItem | undefined {
  if (!v.outdated) return undefined;
  return {
    id: "egg",
    sig: String(v.latest),
    rank: rank.update,
    text: `A newer version of ${v.name} (version ${v.latest}) is available${v.current !== undefined ? `; it runs version ${v.current}` : ""}.`,
    action: { label: "Review the update", to: "settings" },
  };
}

// sortItems puts the worst first; ties keep their order.
export function sortItems(items: AttentionItem[]): AttentionItem[] {
  return items
    .map((it, i) => [it, i] as const)
    .sort((a, b) => a[0].rank - b[0].rank || a[1] - b[1])
    .map(([it]) => it);
}
