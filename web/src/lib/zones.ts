// Time zones, for the node clock in the header: what time it is in a zone,
// how far a zone is from another, and a wall-clock time in one zone as an
// instant.

// timeZones is every IANA zone the browser knows, UTC first.
export function timeZones(): string[] {
  const intl = Intl as typeof Intl & { supportedValuesOf?: (k: string) => string[] };
  return ["UTC", ...(intl.supportedValuesOf?.("timeZone") ?? []).filter((z) => z !== "UTC")];
}

// validZone reports whether the browser knows a zone.
export function validZone(tz: string): boolean {
  try {
    new Intl.DateTimeFormat("en", { timeZone: tz });
    return true;
  } catch {
    return false;
  }
}

// offsetMinutes is how far a zone's wall clock is ahead of UTC at an
// instant (negative behind), daylight saving included.
export function offsetMinutes(tz: string, at: Date): number {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    hourCycle: "h23",
    year: "numeric",
    month: "numeric",
    day: "numeric",
    hour: "numeric",
    minute: "numeric",
    second: "numeric",
  }).formatToParts(at);
  const n = (t: string) => Number(parts.find((p) => p.type === t)?.value ?? 0);
  const wall = Date.UTC(n("year"), n("month") - 1, n("day"), n("hour"), n("minute"), n("second"));
  return Math.round((wall - Math.floor(at.getTime() / 1000) * 1000) / 60_000);
}

// wallTime is the instant a zone's clock shows hours:minutes on the day it
// is there at `on`.
export function wallTime(tz: string, hours: number, minutes: number, on: Date): Date {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    year: "numeric",
    month: "numeric",
    day: "numeric",
  }).formatToParts(on);
  const n = (t: string) => Number(parts.find((p) => p.type === t)?.value ?? 0);
  const guess = Date.UTC(n("year"), n("month") - 1, n("day"), hours, minutes);
  // The offset at the guess, then again at the answer, for a time near a
  // daylight saving change.
  let at = new Date(guess - offsetMinutes(tz, new Date(guess)) * 60_000);
  at = new Date(guess - offsetMinutes(tz, at) * 60_000);
  return at;
}

// difference is how a zone's clock compares with another's: "+1h",
// "−5h 30m", or "same time".
export function difference(tz: string, from: string, at: Date): string {
  const d = offsetMinutes(tz, at) - offsetMinutes(from, at);
  if (d === 0) return "same time";
  const a = Math.abs(d);
  const h = Math.floor(a / 60);
  const m = a % 60;
  return `${d > 0 ? "+" : "−"}${h ? `${h}h` : ""}${h && m ? " " : ""}${m ? `${m}m` : ""}`;
}

// clock is a time in a zone, like "14:05".
export function clock(tz: string, at: Date): string {
  return new Intl.DateTimeFormat(undefined, {
    timeZone: tz,
    hour: "2-digit",
    minute: "2-digit",
  }).format(at);
}

// day is a date in a zone, like "Fri, Oct 16".
export function day(tz: string, at: Date): string {
  return new Intl.DateTimeFormat(undefined, {
    timeZone: tz,
    weekday: "short",
    month: "short",
    day: "numeric",
  }).format(at);
}

// zoneName is a zone's short name at an instant ("CEST", "GMT+2").
export function zoneName(tz: string, at: Date): string {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    timeZoneName: "short",
  }).formatToParts(at);
  return parts.find((p) => p.type === "timeZoneName")?.value ?? tz;
}

// city is the readable end of a zone's name: "America/New_York" is "New York".
export function city(tz: string): string {
  return (tz.split("/").pop() ?? tz).replaceAll("_", " ");
}
