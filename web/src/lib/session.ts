import { Code, ConnectError } from "@connectrpc/connect";
import { redirect } from "@tanstack/react-router";

import type { GetSessionResponse } from "@/gen/raptor/panel/v1/auth_pb";
import { isGuest, setDemoGuest } from "@/lib/demo";
import { guardSession } from "@/lib/session-cache";
import { authClient } from "@/lib/transport";

// currentSession is who's signed in, or null. It notes whether that's a
// demo guest, for the passkey-signing helpers (lib/demo.ts).
export async function currentSession(): Promise<GetSessionResponse | null> {
  try {
    const s = await authClient.getSession({});
    setDemoGuest(isGuest(s.user));
    return s;
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
      setDemoGuest(false);
      return null;
    }
    throw err;
  }
}

// requireSession is a route guard: signed out goes to /signin, and back
// here after. Who's signed in is cached for a moment (lib/session-cache.ts),
// so moving between pages and preloading on hover don't each ask.
export async function requireSession(location: { href: string }): Promise<GetSessionResponse> {
  const s = await guardSession.get(currentSession);
  if (!s) {
    guardSession.clear();
    throw redirect({ to: "/signin", search: { next: location.href } });
  }
  return s;
}

// safeNext keeps post-sign-in redirects inside the app.
export function safeNext(next: unknown): string {
  return typeof next === "string" && next.startsWith("/") && !next.startsWith("//") ? next : "/";
}
