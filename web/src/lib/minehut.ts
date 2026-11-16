// Connecting a Minecraft server to Minehut as an external server (#455):
// what Minehut's guide asks of each kind of server, as edits Raptor can
// make. https://support.minehut.com/hc/en-us/articles/33252734474899

import { withProperty } from "./minecraft-text.ts";

export type MinehutKind = "velocity" | "waterfall" | "paper" | "bungeecord";

// minehutKind is what Minehut's guide calls a server, from its egg's
// plugin loaders (the first is the software itself); undefined for
// servers it has no steps for. BungeeCord isn't supported by Minehut.
export function minehutKind(loaders: string[] | undefined): MinehutKind | undefined {
  const first = loaders?.[0];
  if (first === "velocity") return "velocity";
  if (first === "waterfall") return "waterfall";
  if (first === "bungeecord") return "bungeecord";
  if (first === "paper" || first === "purpur" || first === "folia") return "paper";
  return undefined;
}

const hasJoined = "https://api.minehut.com/mitm/proxy/session/minecraft/hasJoined";

// minehutFlags are the Java flags that send logins through Minehut.
export const minehutFlags: Record<Exclude<MinehutKind, "bungeecord">, string> = {
  velocity: `-Dmojang.sessionserver=${hasJoined}`,
  waterfall: `-Dwaterfall.auth.url="${hasJoined}?username=%s&serverId=%s%s"`,
  paper: [
    "-Dminecraft.api.services.host=https://api.minecraftservices.com/",
    "-Dminecraft.api.auth.host=https://authserver.mojang.com/",
    "-Dminecraft.api.account.host=https://api.mojang.com/",
    "-Dminecraft.api.profiles.host=https://api.mojang.com/",
    "-Dminecraft.api.session.host=https://api.minehut.com/mitm/proxy",
    "-Dminecraft.api.discovery.host=https://api.minehut.com/mitm/proxy/discovery/minecraft/client",
  ].join(" "),
};

// What Minehut's External Server Settings call the proxy type.
export const minehutProxyType: Record<Exclude<MinehutKind, "bungeecord">, string> = {
  velocity: "Velocity",
  waterfall: "Waterfall",
  paper: "Other",
};

// withFlags is a startup command with the flags right after the java
// that launches the server: "exec java" if there is one (the library's
// eggs check the Java version with "java -version" first), else the
// first java that isn't a version check; undefined when there's none.
export function withFlags(startup: string, flags: string): string | undefined {
  if (startup.includes(flags)) return startup;
  const launches = [...startup.matchAll(/(^|[\s;&|])((?:exec\s+)?java)\s+(?!-version\b)/g)];
  const m = launches.find((x) => x[2]?.startsWith("exec")) ?? launches[0];
  if (!m || m.index === undefined) return undefined;
  const at = m.index + (m[1] ?? "").length + (m[2] ?? "").length + 1;
  return `${startup.slice(0, at)}${flags} ${startup.slice(at)}`;
}

// withoutFlags takes the flags back out.
export function withoutFlags(startup: string, flags: string): string {
  return startup.replace(`${flags} `, "").replace(flags, "");
}

// A config file Minehut needs changed: its path, and the change.
export type ConfigEdit = { path: string; apply: (text: string, on: boolean) => string | undefined };

// setLine sets every "key<sep>true|false" line's value; undefined when
// there's none (the file isn't the one expected, or it isn't made yet).
function setLine(key: RegExp, on: boolean) {
  return (text: string): string | undefined => {
    const re = new RegExp(`^(\\s*${key.source}\\s*)(true|false)\\b`, "gm");
    if (!re.test(text)) return undefined;
    return text.replace(re, `$1${on}`);
  };
}

// minehutEdits are the config files to change. tcpShield leaves out the
// proxy protocol, as Minehut's guide says.
export function minehutEdits(
  kind: Exclude<MinehutKind, "bungeecord">,
  tcpShield: boolean,
): ConfigEdit[] {
  const out: ConfigEdit[] = [];
  if (kind === "velocity" && !tcpShield) {
    out.push({ path: "velocity.toml", apply: (t, on) => setLine(/haproxy-protocol\s*=/, on)(t) });
  }
  if (kind === "waterfall" && !tcpShield) {
    out.push({ path: "config.yml", apply: (t, on) => setLine(/proxy_protocol:/, on)(t) });
  }
  if (kind === "paper") {
    if (!tcpShield) {
      out.push({
        path: "config/paper-global.yml",
        apply: (t, on) => setLine(/proxy-protocol:/, on)(t),
      });
    }
    out.push({
      path: "server.properties",
      // Minehut's login doesn't sign chat, so the server mustn't demand it.
      apply: (t, on) => withProperty(t, "enforce-secure-profile", on ? "false" : "true"),
    });
  }
  return out;
}
