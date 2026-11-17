import { Code, ConnectError } from "@connectrpc/connect";
import { useSyncExternalStore } from "react";

// Whether the app can reach the Panel: requests that fail because it's
// unreachable mark it down, and the next one that gets through marks it
// up, so the app can say so once instead of on every card.

let down = false;
const listeners = new Set<() => void>();

function set(next: boolean) {
  if (next === down) return;
  down = next;
  for (const l of listeners) l();
}

// fetch's own failure when nothing answers, as Chrome, Firefox and Safari
// word it. Only these: any other TypeError is a bug in the app, not the
// network.
function fetchFailed(err: unknown): boolean {
  return (
    err instanceof TypeError &&
    /^(Failed to fetch|NetworkError when attempting to fetch resource\.|Load failed|network error)$/.test(
      err.message,
    )
  );
}

// unreachable reports whether an error means the Panel couldn't be reached
// (it's down, or the network is), rather than that it answered. An error
// the Panel wrote is an answer even when its code is "unavailable": that's
// the Panel saying a node is offline, or a service isn't set up, and the
// page should say so instead of claiming Raptor is down.
export function unreachable(err: unknown): boolean {
  if (err instanceof ConnectError) {
    return (
      // connect wraps fetch's failure, keeping it as the cause.
      fetchFailed(err.cause) ||
      // A proxy's own error page (Caddy's 502, Cloudflare's 520 to 530)
      // has no Connect body, so only the HTTP status comes through.
      /^HTTP 5\d\d$/.test(err.rawMessage) ||
      // The client's own deadline (lib/transport.ts), not the Panel's.
      (err.code === Code.DeadlineExceeded && err.rawMessage === "the operation timed out")
    );
  }
  return fetchFailed(err);
}

export function reportResult(err: unknown) {
  set(err !== undefined && err !== null && unreachable(err));
}

export function useApiDown(): boolean {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => down,
  );
}
