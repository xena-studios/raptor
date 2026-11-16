// Counter-Strike 2's plugin frameworks (#414, docs/EGGS.md#cs2-frameworks):
// Metamod:Source and CounterStrikeSharp. The egg's install script
// downloads them and its startup loads them; this only edits the
// variables that turn them on and pin their versions.

export const frameworkVars = {
  metamod: "METAMOD",
  metamodVersion: "METAMOD_VERSION",
  css: "COUNTERSTRIKESHARP",
  cssVersion: "CSS_VERSION",
} as const;

export type Frameworks = {
  metamod: boolean;
  metamodVersion: string;
  css: boolean;
  cssVersion: string;
};

// hasFrameworks says whether an egg (by its variables' names) takes them.
export function hasFrameworks(envs: string[]): boolean {
  return Object.values(frameworkVars).every((v) => envs.includes(v));
}

const on = (v: string | undefined) => v === "1" || v === "true";

// frameworks reads the server's choices, the egg's defaults where unset.
export function frameworks(
  values: Record<string, string> | undefined,
  defaults: Record<string, string>,
): Frameworks {
  const get = (k: string) => values?.[k] ?? defaults[k] ?? "";
  return {
    metamod: on(get(frameworkVars.metamod)),
    metamodVersion: get(frameworkVars.metamodVersion) || "latest",
    css: on(get(frameworkVars.css)),
    cssVersion: get(frameworkVars.cssVersion) || "latest",
  };
}

// The egg's rules for the versions: latest, a Metamod build number
// (1491), a CounterStrikeSharp release (1.0.380 or v1.0.380).
export function versionError(which: "metamod" | "css", v: string): string {
  if (v === "latest") return "";
  if (which === "metamod") {
    return /^[0-9]{1,10}$/.test(v) ? "" : "A build number, such as 1491, or latest.";
  }
  return /^v?[0-9]+(\.[0-9]+)*$/.test(v) && v.length <= 20
    ? ""
    : "A release, such as 1.0.380, or latest.";
}

// Metamod loads when it's on or CounterStrikeSharp is (which runs on it).
export const metamodLoads = (f: Frameworks) => f.metamod || f.css;

// change is what saving `next` over `saved` sends, and whether the
// install has to run for it: anything turned on or a version changed
// while on. Turning off needs only a restart.
export function change(
  saved: Frameworks,
  next: Frameworks,
): { variables: Record<string, string>; reinstall: boolean } {
  const variables: Record<string, string> = {};
  if (next.metamod !== saved.metamod) variables[frameworkVars.metamod] = next.metamod ? "1" : "0";
  if (next.css !== saved.css) variables[frameworkVars.css] = next.css ? "1" : "0";
  if (next.metamodVersion !== saved.metamodVersion)
    variables[frameworkVars.metamodVersion] = next.metamodVersion;
  if (next.cssVersion !== saved.cssVersion) variables[frameworkVars.cssVersion] = next.cssVersion;
  const reinstall =
    (metamodLoads(next) &&
      (!metamodLoads(saved) || next.metamodVersion !== saved.metamodVersion)) ||
    (next.css && (!saved.css || next.cssVersion !== saved.cssVersion));
  return { variables, reinstall };
}
