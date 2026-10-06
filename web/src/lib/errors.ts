import { Code, ConnectError } from "@connectrpc/connect";

// message is what to show for an API error: the Panel's own words, which
// are written for users.
export function message(err: unknown): string {
  if (err instanceof ConnectError) {
    if (err.code === Code.Unavailable && !err.rawMessage)
      return "The Panel can't be reached. Try again in a moment.";
    return err.rawMessage || "Something went wrong.";
  }
  if (err instanceof Error) return err.message;
  return "Something went wrong.";
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
