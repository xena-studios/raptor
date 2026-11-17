// The command menu's steps (#436): "Send a command" → pick a server →
// type the command. The menu keeps a stack of steps; each has its own
// query (so going back restores what you'd typed), breadcrumbs name them,
// and Backspace in an empty input goes back one. Pure, so it's tested in
// node.

import type { ServerAct } from "./command-items.ts";

export type Step =
  | { kind: "root" }
  // Choose the server to do act to.
  | { kind: "pick"; act: ServerAct }
  // A server's, node's, or egg's own commands.
  | { kind: "server"; serverId: string }
  | { kind: "node"; nodeId: string }
  | { kind: "egg"; eggId: string }
  // Choose one of a server's schedules to run.
  | { kind: "schedule"; serverId: string }
  // Type the value an act needs (a command, a player, a path).
  | { kind: "input"; act: ServerAct; serverId: string }
  // Type the email to invite.
  | { kind: "invite" };

export type Frame = { step: Step; query: string };

export type Stack = readonly Frame[];

export const rootStack: Stack = [{ step: { kind: "root" }, query: "" }];

export function current(s: Stack): Frame {
  return s[s.length - 1] ?? { step: { kind: "root" }, query: "" };
}

// push goes into a step with an empty query (an input step may start
// with a value).
export function push(s: Stack, step: Step, query = ""): Stack {
  return [...s, { step, query }];
}

// back leaves the current step for the one before, with its query as it
// was; at the root it stays.
export function back(s: Stack): Stack {
  return s.length > 1 ? s.slice(0, -1) : s;
}

// setQuery is the stack with the current step's query changed.
export function setQuery(s: Stack, query: string): Stack {
  const top = current(s);
  return [...s.slice(0, -1), { ...top, query }];
}

// onBackspace is the stack after Backspace with this query: back a step
// when the input is already empty and there's a step to go back to;
// undefined when Backspace should edit the text as usual.
export function onBackspace(s: Stack, query: string): Stack | undefined {
  return query === "" && s.length > 1 ? back(s) : undefined;
}

// Names: what a step's breadcrumb says, from what it's about.
export type Names = {
  server: (id: string) => string;
  node: (id: string) => string;
  egg: (id: string) => string;
  // "Send a console command…" for a pick step's act, "Send a command" for
  // an input step's.
  verb: (act: ServerAct) => string;
  short: (act: ServerAct) => string;
};

function crumb(step: Step, n: Names): string {
  switch (step.kind) {
    case "root":
      return "Search";
    case "pick":
      return n.verb(step.act).replace(/…$/, "");
    case "server":
      return n.server(step.serverId);
    case "node":
      return n.node(step.nodeId);
    case "egg":
      return n.egg(step.eggId);
    case "schedule":
      return "Run a schedule";
    case "input":
      return n.short(step.act).replace(/…$/, "");
    case "invite":
      return "Invite a member";
  }
}

// crumbs is the breadcrumbs: one per step after the root. A pick step
// followed by its server's step reads as the server.
export function crumbs(s: Stack, n: Names): string[] {
  const out: string[] = [];
  for (const [i, f] of s.entries()) {
    if (f.step.kind === "root") continue;
    // The pick step already named what's being done.
    const prev = s[i - 1]?.step;
    if (prev?.kind === "pick" && (f.step.kind === "input" || f.step.kind === "schedule")) continue;
    out.push(crumb(f.step, n));
    // After picking a server, name it too.
    const next = s[i + 1]?.step;
    if (f.step.kind === "pick" && next && "serverId" in next) out.push(n.server(next.serverId));
  }
  return out;
}

// placeholder is the input's prompt for the current step.
export function placeholder(step: Step, inputPlaceholder?: string): string {
  switch (step.kind) {
    case "root":
      return "Search pages, servers, and actions…";
    case "pick":
      return "Pick a server…";
    case "server":
    case "node":
    case "egg":
      return "Search its commands…";
    case "schedule":
      return "Pick a schedule…";
    case "input":
      return inputPlaceholder ?? "Type…";
    case "invite":
      return "Their email address";
  }
}

// isInput says whether the input holds a value, not a search.
export function isInput(step: Step): boolean {
  return step.kind === "input" || step.kind === "invite";
}
