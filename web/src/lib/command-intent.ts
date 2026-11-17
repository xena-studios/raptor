// Hand-offs from the command menu to a page (#436). Some actions are the
// page's own work, with its checks and its way of showing the result:
// updating every add-on (the Add-ons tab confirms the list), whitelisting
// a player (the Players tab knows whether to tell the game or edit its
// files), testing the connection, sharing the console. The menu leaves an
// intent here and opens the page; the page takes it once, when it's
// ready. It lives in memory only, never in the URL, so a link can't make
// a page act, and it expires if the page never takes it.

export type Intent =
  | { kind: "addons.update"; serverId: string }
  | { kind: "players"; serverId: string; op: "whitelist" | "op" | "kick"; name: string }
  | { kind: "connection.test"; serverId: string }
  | { kind: "log.share"; serverId: string }
  | { kind: "member.invite"; orgId: string; email: string };

export type IntentKind = Intent["kind"];

// How long an intent waits for its page: long enough for a slow load.
export const intentLifetime = 30_000;

let pending: { intent: Intent; at: number } | undefined;

// leaveIntent sets the intent for the next page to take, replacing any
// other.
export function leaveIntent(intent: Intent, now = Date.now()): void {
  pending = { intent, at: now };
}

// takeIntent returns the pending intent of this kind for this server (or
// org) and clears it; undefined if there's none, it's another's, or it's
// too old.
export function takeIntent<K extends IntentKind>(
  kind: K,
  scope: string,
  now = Date.now(),
): Extract<Intent, { kind: K }> | undefined {
  const p = pending;
  if (!p) return undefined;
  if (now - p.at > intentLifetime) {
    pending = undefined;
    return undefined;
  }
  const i = p.intent;
  if (i.kind !== kind) return undefined;
  const target = "serverId" in i ? i.serverId : i.orgId;
  if (target !== scope) return undefined;
  pending = undefined;
  return i as Extract<Intent, { kind: K }>;
}

// hasIntent says whether one is waiting for this kind and scope, without
// taking it (a page checks before it starts loading what it needs).
export function hasIntent(kind: IntentKind, scope: string, now = Date.now()): boolean {
  const p = pending;
  if (!p || now - p.at > intentLifetime || p.intent.kind !== kind) return false;
  const i = p.intent;
  return ("serverId" in i ? i.serverId : i.orgId) === scope;
}
