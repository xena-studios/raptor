// A short-lived cache of who's signed in, for the route guards, so moving
// between pages doesn't wait on GetSession each time. It only decides
// redirects: the API still checks the session cookie on every request.
// Anything that can change the session clears it (lib/transport.ts):
// every AuthService call that isn't a read, and any request the API
// answers with "unauthenticated" (docs/DECISIONS.md #385).

import type { GetSessionResponse } from "@/gen/raptor/panel/v1/auth_pb";

export type SessionCache<T> = {
  // get is the cached value while it's fresh, or load's, shared by the
  // callers that ask while it's loading. A failed load isn't kept.
  get(load: () => Promise<T>): Promise<T>;
  // clear forgets the value, and any load still running: its callers get
  // its answer, later ones load again.
  clear(): void;
};

export function sessionCache<T>(ttlMs: number, now: () => number = Date.now): SessionCache<T> {
  let entry: { at: number; value: Promise<T> } | undefined;
  return {
    get(load) {
      if (entry && now() - entry.at < ttlMs) return entry.value;
      const mine = { at: now(), value: load() };
      entry = mine;
      mine.value.catch(() => {
        if (entry === mine) entry = undefined;
      });
      return mine.value;
    },
    clear() {
      entry = undefined;
    },
  };
}

// The route guards' cache: 30 seconds. null (signed out) isn't kept
// (lib/session.ts).
export const guardSession = sessionCache<GetSessionResponse | null>(30_000);
