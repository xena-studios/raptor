// Problems a server's console shows that the app can name and offer a fix
// for (#430). Each rule matches a console line; the newest match among the
// last lines wins, so a problem that's since been fixed (the server ran
// after it) drops out.

export type Problem =
  | {
      kind: "java";
      // The Java the server needs, and whether a newer one also works.
      need: number;
      orNewer: boolean;
      // The console line that showed it.
      line: string;
    }
  | {
      kind: "hint";
      // Which rule (hints[].id).
      id: string;
      title: string;
      text: string;
      // Where the fix is: one of the server's tabs.
      action?: { label: string; tab: HintTab };
      // Shown while the server runs too (a plugin that didn't load);
      // otherwise only once it's stopped or crashed.
      whileRunning: boolean;
      line: string;
    };

export type HintTab = "network" | "settings" | "files" | "addons/mods" | "addons/plugins";

// How many of the newest lines are looked at: enough to reach back to a
// plugin that failed while a server started.
export const problemWindow = 200;

type Hint = Omit<Extract<Problem, { kind: "hint" }>, "kind" | "line"> & { match: RegExp };

// hints are console lines with a known cause and fix, beyond the Java
// rule. Each says what's wrong in a sentence and where to fix it.
export const hints: Hint[] = [
  {
    id: "oom",
    match: /^\[raptor\] crashed \(exit code \d+, oom\)|java\.lang\.OutOfMemoryError/,
    title: "The server ran out of memory",
    text: "It needed more memory than it's allowed. Give it more on Settings (Name and limits), or remove some mods or plugins.",
    action: { label: "Open Settings", tab: "settings" },
    whileRunning: false,
  },
  {
    id: "port",
    match: /FAILED TO BIND TO PORT|Address already in use|EADDRINUSE/,
    title: "Its port is already in use",
    text: "Something else on the machine, often another server, is using the same port. Give this server another port on Network, or stop the other one.",
    action: { label: "Open Network", tab: "network" },
    whileRunning: false,
  },
  {
    id: "eula",
    match: /You need to agree to the EULA in order to run the server/,
    title: "Minecraft's EULA isn't accepted",
    text: "Minecraft won't start until its EULA is accepted. Open eula.txt in Files, set eula=true, and start the server again.",
    action: { label: "Open Files", tab: "files" },
    whileRunning: false,
  },
  {
    id: "fabric-deps",
    match:
      /requires (?:version .+ of|any version of) .+, which is missing!|Incompatible mods? (?:found|set)!/,
    title: "A mod is missing something it needs",
    text: "A mod needs another mod (often Fabric API) that isn't installed, or two mods can't run together. The Mods tab can install what's missing.",
    action: { label: "Open Mods", tab: "addons/mods" },
    whileRunning: false,
  },
  {
    id: "forge-deps",
    match: /Missing or unsupported mandatory dependencies/,
    title: "A mod is missing something it needs",
    text: "A mod needs another mod that isn't installed, or a different version of one. The Mods tab can install what's missing.",
    action: { label: "Open Mods", tab: "addons/mods" },
    whileRunning: false,
  },
  {
    id: "plugin-deps",
    match: /UnknownDependencyException|Unknown\/missing dependency plugins/,
    title: "A plugin didn't load: it's missing another plugin",
    text: "A plugin needs another plugin that isn't installed, so it was skipped. The console line names it; install it from the Plugins tab.",
    action: { label: "Open Plugins", tab: "addons/plugins" },
    whileRunning: true,
  },
  {
    id: "plugin-version",
    match:
      /Could not load '[^']+' in folder '[^']+'.*(?:InvalidPluginException|Unsupported API version)/,
    title: "A plugin didn't load",
    text: "A plugin couldn't load, often because it's made for another Minecraft version. The console line names it; update or remove it on the Plugins tab.",
    action: { label: "Open Plugins", tab: "addons/plugins" },
    whileRunning: true,
  },
  {
    id: "crash-loop",
    match: /^\[raptor\] crashed \d+ times in .+; not restarting/,
    title: "It keeps crashing, so it isn't restarted",
    text: "The server crashed several times in a row. Look at the console just before the crashes for the reason, then start it again.",
    whileRunning: false,
  },
];

// The library's Minecraft eggs refuse to start on the wrong Java with
// "… Choose Java 21 in Startup, then start the server again." (and "Choose
// Java 21 or newer").
const libraryJava = /Choose Java (\d+)( or newer)? in Startup, then start the server again/;
// Any Java program compiled for a newer Java than the one running it:
// "UnsupportedClassVersionError: … class file version 65.0". Class file
// version 52 is Java 8, and each Java since adds one.
const classVersion = /UnsupportedClassVersionError.*class file version (\d+)(?:\.\d+)?/;

// findProblem is the newest problem in a server's console output.
export function findProblem(lines: string[]): Problem | undefined {
  const start = Math.max(0, lines.length - problemWindow);
  for (let i = lines.length - 1; i >= start; i--) {
    // Colour codes would split the text a rule looks for.
    // biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI escapes
    const line = (lines[i] ?? "").replace(/\x1b\[[0-9;]*[A-Za-z]/g, "");
    let m = libraryJava.exec(line);
    if (m) return { kind: "java", need: Number(m[1]), orNewer: !!m[2], line };
    m = classVersion.exec(line);
    if (m && Number(m[1]) >= 52) {
      return { kind: "java", need: Number(m[1]) - 44, orNewer: true, line };
    }
    const hint = hints.find((h) => h.match.test(line));
    if (hint) {
      const { match: _, ...rest } = hint;
      return { kind: "hint", ...rest, line };
    }
  }
  return undefined;
}

// javaOf is the Java version an egg's image is for, from its name ("Java
// 21") or its reference (yolks:java_21); 0 when it doesn't say.
export function javaOf(image: { name: string; ref: string }): number {
  const m = /\bjava\s*(\d+)\b/i.exec(image.name) ?? /java[_-]?(\d+)/i.exec(image.ref);
  return m ? Number(m[1]) : 0;
}

// javaImage is the egg's image to switch to: the Java asked for, or with
// orNewer the oldest one that's new enough (the closest to what was asked).
export function javaImage<T extends { name: string; ref: string }>(
  images: T[],
  need: number,
  orNewer: boolean,
): T | undefined {
  const fits = images
    .map((i) => ({ i, v: javaOf(i) }))
    .filter(({ v }) => v === need || (orNewer && v > need))
    .sort((a, b) => a.v - b.v);
  return fits[0]?.i;
}
