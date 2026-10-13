import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import { Role } from "@/gen/raptor/panel/v1/org_pb";

// dateTime and dateOnly are how the app writes dates: "Oct 12, 2026, 9:00 PM"
// and "Oct 12, 2026", in the reader's locale. Seconds are never worth reading.
export function dateTime(d: Date): string {
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function dateOnly(d: Date): string {
  return d.toLocaleDateString(undefined, { dateStyle: "medium" });
}

// ago is "5 minutes ago", "yesterday", or, past a week, the date.
export function ago(d: Date): string {
  const s = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} minute${m === 1 ? "" : "s"} ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} hour${h === 1 ? "" : "s"} ago`;
  const days = Math.floor(h / 24);
  if (days === 1) return "yesterday";
  if (days < 7) return `${days} days ago`;
  return dateOnly(d);
}

// since is ago for a timestamp: "never" when there isn't one.
export function since(ts: Timestamp | undefined): string {
  return ts ? ago(timestampDate(ts)) : "never";
}

export function when(ts: Timestamp | undefined): string {
  return ts ? dateTime(timestampDate(ts)) : "never";
}

export const roleNames: Record<Role, string> = {
  [Role.UNSPECIFIED]: "—",
  [Role.MEMBER]: "Member",
  [Role.ADMIN]: "Admin",
  [Role.OWNER]: "Owner",
};

export function isAdmin(role: Role | undefined): boolean {
  return role === Role.ADMIN || role === Role.OWNER;
}
