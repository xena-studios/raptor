// Problems a server's console shows that the app can name and offer a fix
// for (#430). Each rule matches a console line; the newest match among the
// last lines wins, so a problem that's since been fixed (the server ran
// after it) drops out.

export type Problem = {
  kind: "java";
  // The Java the server needs, and whether a newer one also works.
  need: number;
  orNewer: boolean;
  // The console line that showed it.
  line: string;
};

// How many of the newest lines are looked at.
export const problemWindow = 60;

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
