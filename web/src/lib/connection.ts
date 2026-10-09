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

// unreachable reports whether an error means the Panel couldn't be reached
// (it's down, or the network is), rather than that it answered.
export function unreachable(err: unknown): boolean {
  if (err instanceof ConnectError) {
    // Cloudflare's own errors for an origin that's down (520 to 530) have
    // no Connect body, so they arrive as "unknown" with the HTTP status.
    return (
      err.code === Code.Unavailable ||
      err.code === Code.DeadlineExceeded ||
      (err.code === Code.Unknown && /^HTTP 5\d\d$/.test(err.rawMessage))
    );
  }
  // fetch's own failure when nothing answers.
  return err instanceof TypeError;
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
