// Links that open the app. net.raptorpanel.app:/oauth?code=… is the sign-in
// coming back from the web app: the browser session in lib/auth.ts takes
// it, so the router just stays where it is (the start, which is sign-in or
// the inbox). Never throw here: it runs before anything renders.
export function redirectSystemPath({ path }: { path: string; initial: boolean }): string {
  try {
    if (path.includes("oauth")) return "/";
    return path;
  } catch {
    return "/";
  }
}
