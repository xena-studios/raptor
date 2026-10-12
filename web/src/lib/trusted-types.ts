// Trusted Types (the CSP's require-trusted-types-for 'script'): the app
// never passes a plain string to a sink that runs code or markup. Its own
// code inserts no markup; Monaco and xterm bring their own policies. This
// one is for the editor's workers: only the app's own built files.

type ScriptURLPolicy = { createScriptURL(url: string): string };

declare global {
  interface Window {
    trustedTypes?: {
      createPolicy(name: string, rules: { createScriptURL(url: string): string }): ScriptURLPolicy;
    };
  }
}

let policy: ScriptURLPolicy | undefined;

// workerURL is a worker's script URL that Trusted Types accepts: the app's
// own origin, and in a build its /assets/ only. Browsers without Trusted
// Types get the URL as it is.
export function workerURL(url: string): string {
  if (!window.trustedTypes) return url;
  policy ??= window.trustedTypes.createPolicy("raptor-workers", {
    createScriptURL(u) {
      const parsed = new URL(u, window.location.href);
      if (
        parsed.origin !== window.location.origin ||
        (!import.meta.env.DEV && !parsed.pathname.startsWith("/assets/"))
      ) {
        throw new TypeError(`not one of the app's workers: ${u}`);
      }
      return parsed.href;
    },
  });
  return policy.createScriptURL(url);
}
