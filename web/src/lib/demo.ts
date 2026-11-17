// The public demo (#449, docs/PANEL.md#the-public-demo): guests try a real
// Panel in Raptor's demo org. The Panel refuses what's off there; the app
// only says so before trying, and shows the banner.

export const getRaptorURL = "https://raptorpanel.net";

export const demoBanner = "You're in the demo. It resets every hour.";

// What a passkey-signed button says in the demo instead of asking for a
// passkey (guests have none, and the Panel refuses signed commands there).
export const demoSigned = "In your own Raptor, your passkey signs this.";

// The demo page's states: checking, closed (no demo org yet), ready to
// start, starting, or failed with the Panel's words.
export type DemoState =
  | { kind: "checking" }
  | { kind: "closed" }
  | { kind: "signed-in" }
  | { kind: "ready" }
  | { kind: "starting" }
  | { kind: "failed"; message: string };

// demoState is what the demo page shows, from whether a demo org is open,
// who's signed in, and the last attempt.
export function demoState(opts: {
  open: boolean | undefined;
  signedIn: "none" | "guest" | "account";
  starting: boolean;
  error: string;
}): DemoState {
  if (opts.signedIn === "account") return { kind: "signed-in" };
  if (opts.open === undefined) return { kind: "checking" };
  if (!opts.open) return { kind: "closed" };
  if (opts.starting) return { kind: "starting" };
  if (opts.error) return { kind: "failed", message: opts.error };
  return { kind: "ready" };
}

// isGuest reports whether a session's user is a demo guest.
export function isGuest(user: { guestUntil?: unknown } | undefined): boolean {
  return user?.guestUntil !== undefined;
}

// showBanner reports whether a page shows the demo banner: for guests
// everywhere, and in the demo org for anyone (its staff owners too).
export function showBanner(guest: boolean, orgDemo: boolean | undefined): boolean {
  return guest || orgDemo === true;
}

// The signed-in user is a guest: set from the session, read by the
// passkey-signing helpers, which have no React context.
let guestNow = false;

export function setDemoGuest(on: boolean) {
  guestNow = on;
}

export function demoGuest(): boolean {
  return guestNow;
}

// refuseSigning throws demoSigned for a guest, before any passkey prompt.
export function refuseSigning() {
  if (guestNow) throw new Error(demoSigned);
}

// ─── What the demo refuses ──────────────────────────────────────────────────

// The Panel's deny list for commands in the demo org (internal/panel/perms/
// demo.go DemoRefuses), so menus and buttons can hide what it refuses
// before anyone tries. The Panel is what enforces it.
const deniedPrefixes = [
  "node.",
  "support.",
  "keys.",
  "files.pull",
  "files.import",
  "sftp.",
  "backup.destination",
  "backup.key",
];

const deniedActions = new Set([
  "server.create",
  "server.delete",
  "server.move",
  "server.reinstall",
  "server.networks",
  "backup.restore",
  "backup.delete",
  "backup.lock",
  "backup.recover",
  "backup.orphans",
  "backup.hostkey",
  "backup.policy.update",
  "db.password",
  "db.password.reset",
  "db.readonly.set",
  "db.readonly.remove",
]);

function present(v: unknown): boolean {
  return v !== undefined && v !== null && v !== "";
}

// demoHides reports whether the demo refuses a command (for everyone in
// the demo org but its owners): params matter for backup.create (a locked
// backup) and server.update (a new egg, or a reinstall).
export function demoHides(action: string, params?: unknown): boolean {
  if (deniedActions.has(action)) return true;
  if (deniedPrefixes.some((p) => action.startsWith(p))) return true;
  const p = (params && typeof params === "object" ? params : {}) as Record<string, unknown>;
  if (action === "backup.create") return p.locked === true;
  if (action === "server.update") return present(p.egg) || present(p.reinstall);
  return false;
}

// The largest file a guest can upload in the demo (demo.UploadLimit).
export const demoUploadLimit = 10 << 20;

// ─── Sandboxes ──────────────────────────────────────────────────────────────

// Sandbox states: from the queue to its deletion.
export type SandboxState = "queued" | "creating" | "installing" | "ready" | "gone" | "failed";

// liveSandbox reports whether a sandbox is queued or running (not over).
export function liveSandbox(state: string | undefined): boolean {
  return state === "queued" || state === "creating" || state === "installing" || state === "ready";
}

// sandboxPollMs is how often a sandbox's state is asked for: often while
// it's waiting or installing, now and then once it's ready, not at all
// once it's over.
export function sandboxPollMs(state: string | undefined): number | false {
  if (state === "queued" || state === "creating" || state === "installing") return 5_000;
  if (state === "ready") return 30_000;
  return false;
}

// countdown is the time left as m:ss (h:mm:ss past an hour), never below
// 0:00.
export function countdown(deadlineMs: number, nowMs: number): string {
  const s = Math.max(0, Math.floor((deadlineMs - nowMs) / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

// queueText says where a queued sandbox is: "Next in the queue" or "#3 in
// the queue", and about how long.
export function queueText(position: number, waitSeconds: number): string {
  const where = position <= 0 ? "Next in the queue" : `#${position + 1} in the queue`;
  if (waitSeconds < 60) return `${where}, starting soon`;
  const min = Math.round(waitSeconds / 60);
  return `${where}, about ${min} min`;
}

// sandboxStatus is the banner's words for a sandbox.
export function sandboxStatus(s: {
  state: string;
  eggName: string;
  queuePosition: number;
  waitSeconds: number;
  error: string;
}): string {
  switch (s.state) {
    case "queued":
      return `${s.eggName}: ${queueText(s.queuePosition, s.waitSeconds)}`;
    case "creating":
    case "installing":
      return `Installing ${s.eggName}…`;
    case "ready":
      return s.eggName;
    case "failed":
      return `${s.eggName} couldn't be made${s.error ? `: ${s.error}` : ""}`;
    default:
      return `${s.eggName} was deleted`;
  }
}

// DemoEggRow is a catalog egg with whether it fits the demo.
export type DemoEggRow<E> = { egg: E; fits: boolean; why: string };

// demoEggs merges the catalog with the demo's list: only the eggs the demo
// lists (the official library's, on the node's CPU), those that fit first,
// each group by the catalog's popularity, then name.
export function demoEggs<E extends { id: string; name: string }>(
  catalog: E[],
  demo: { eggId: string; fits: boolean; why: string }[],
  popularity: (e: E) => number,
): DemoEggRow<E>[] {
  const byId = new Map(catalog.map((e) => [e.id, e]));
  const rows: DemoEggRow<E>[] = [];
  for (const d of demo) {
    const egg = byId.get(d.eggId);
    if (egg) rows.push({ egg, fits: d.fits, why: d.why });
  }
  return rows.sort(
    (a, b) =>
      Number(b.fits) - Number(a.fits) ||
      popularity(b.egg) - popularity(a.egg) ||
      a.egg.name.localeCompare(b.egg.name),
  );
}

// demoPage is the route a guest is on, as the demo's analytics count it:
// the route's template, with no IDs in it, or "" if it has any.
export function demoPage(template: string): string {
  const p = template.replace(/\/$/, "") || "/";
  return /^\/[a-zA-Z$_./-]{0,120}$/.test(p) ? p : "";
}
