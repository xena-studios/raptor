import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampDate } from "@bufbuild/protobuf/wkt";

import { Role } from "@/gen/raptor/panel/v1/org_pb";

export function when(ts: Timestamp | undefined): string {
  return ts ? timestampDate(ts).toLocaleString() : "never";
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
