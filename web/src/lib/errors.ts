import { Code, ConnectError } from "@connectrpc/connect";

import { unreachable } from "@/lib/connection";

// Codes whose messages the Panel writes for people (and Wings, for a
// command that ran and failed): everything else ("unknown", "internal")
// can carry an error meant for us, so it's said in our own words instead.
const userCodes = new Set([
  Code.InvalidArgument,
  Code.NotFound,
  Code.AlreadyExists,
  Code.PermissionDenied,
  Code.FailedPrecondition,
  Code.ResourceExhausted,
  Code.Unauthenticated,
  Code.OutOfRange,
  Code.Unimplemented,
  Code.Aborted,
]);

// message is what to show for an error: the Panel's own words when they're
// written for users, a plain sentence otherwise. Never a stack trace,
// "Failed to fetch", or a server's internal error.
export function message(err: unknown): string {
  if (err instanceof ConnectError) {
    if (unreachable(err)) {
      return "Raptor can't be reached right now. Check your connection and try again.";
    }
    if (err.code === Code.Canceled) return "That was cancelled.";
    if (userCodes.has(err.code) && err.rawMessage) {
      const m = err.rawMessage;
      return m.charAt(0).toUpperCase() + m.slice(1);
    }
    return "Something went wrong on our side. Try again in a moment.";
  }
  // fetch failing outright: the network or the Panel is down.
  if (unreachable(err)) {
    return "Raptor can't be reached right now. Check your connection and try again.";
  }
  // The browser's passkey errors read like developer docs.
  if (err instanceof DOMException) {
    return "Your passkey didn't answer. Try again, and pick a passkey on this account.";
  }
  // Our own errors (thrown with sentences for people) say what they mean.
  if (err instanceof Error && err.message && /^[A-Z][^\n]*[.!?]$/.test(err.message)) {
    return err.message;
  }
  return "Something went wrong. Try again in a moment.";
}

export function isCode(err: unknown, code: Code): boolean {
  return err instanceof ConnectError && err.code === code;
}

// needsReauth is the Panel asking to confirm it's you before a sensitive
// change.
export function needsReauth(err: unknown): boolean {
  return (
    isCode(err, Code.FailedPrecondition) &&
    err instanceof ConnectError &&
    err.rawMessage === "confirm it's you first"
  );
}
