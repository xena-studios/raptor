import { Code, ConnectError } from "@connectrpc/connect";
import { redirect } from "@tanstack/react-router";

import type { GetSessionResponse } from "@/gen/raptor/panel/v1/auth_pb";
import { authClient } from "@/lib/transport";

// currentSession is who's signed in, or null.
export async function currentSession(): Promise<GetSessionResponse | null> {
  try {
    return await authClient.getSession({});
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.Unauthenticated) return null;
    throw err;
  }
}

// requireSession is a route guard: signed out goes to /signin, and back
// here after.
export async function requireSession(location: { href: string }): Promise<GetSessionResponse> {
  const s = await currentSession();
  if (!s) throw redirect({ to: "/signin", search: { next: location.href } });
  return s;
}

// safeNext keeps post-sign-in redirects inside the app.
export function safeNext(next: unknown): string {
  return typeof next === "string" && next.startsWith("/") && !next.startsWith("//") ? next : "/";
}
