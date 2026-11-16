import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";

// ago is "3m ago", "2h ago", "4d ago", or the date.
export function ago(ts?: Timestamp): string {
  if (!ts) return "";
  const d = timestampDate(ts);
  const s = Math.max(0, (Date.now() - d.getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 7 * 86400) return `${Math.floor(s / 86400)}d ago`;
  return d.toLocaleDateString();
}

// can reports whether the server's permissions allow perm ("*": all).
export function can(permissions: string[], perm: string): boolean {
  return permissions.includes("*") || permissions.includes(perm);
}
