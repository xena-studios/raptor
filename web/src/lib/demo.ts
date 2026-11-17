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
