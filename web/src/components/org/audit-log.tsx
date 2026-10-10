import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import {
  Activity,
  Archive,
  Building2,
  CalendarClock,
  Egg,
  FileText,
  FolderKey,
  KeyRound,
  LogOut,
  type LucideIcon,
  Mail,
  Power,
  RefreshCw,
  Server,
  Settings,
  SquareTerminal,
  Trash2,
  UserMinus,
  UserPlus,
  Users,
} from "lucide-react";

import { describeDevice } from "@/components/account/security";
import { ActivityCard, type TimelineItem, type TimelinePage } from "@/components/activity-timeline";
import type { AuditEvent } from "@/gen/raptor/panel/v1/audit_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { useOrgNodes, useOrgServers } from "@/lib/org-data";
import { orgClient } from "@/lib/transport";

type Meta = Record<string, unknown>;

const str = (v: unknown) => (typeof v === "string" ? v : "");

const roleWords: Record<string, string> = {
  owner: "an owner",
  admin: "an admin",
  member: "a member",
};

// Commands, as what was done: a verb phrase about the server (and its icon).
function describeCommand(action: string, server: string, meta: Meta): [string, LucideIcon] {
  switch (action) {
    case "server.start":
      return [`started ${server}`, Power];
    case "server.stop":
      return [`stopped ${server}`, Power];
    case "server.restart":
      return [`restarted ${server}`, RefreshCw];
    case "server.kill":
      return [`killed ${server}`, Power];
    case "server.command":
      return [`sent a console command to ${server}`, SquareTerminal];
    case "server.create":
      return [`created ${server}`, Server];
    case "server.delete":
      return [`deleted ${server}`, Trash2];
    case "server.update":
      return [`changed ${server}'s settings`, Settings];
    case "server.reinstall":
      return [`reinstalled ${server}`, RefreshCw];
    case "files.write":
      return [`saved a file on ${server}`, FileText];
    case "files.mkdir":
      return [`made a folder on ${server}`, FileText];
    case "files.rename":
      return [`renamed or moved files on ${server}`, FileText];
    case "files.copy":
      return [`copied files on ${server}`, FileText];
    case "files.delete":
      return [`deleted files on ${server}`, Trash2];
    case "files.chmod":
      return [`changed file permissions on ${server}`, FileText];
    case "files.compress":
      return [`compressed files on ${server}`, Archive];
    case "files.decompress":
      return [`extracted an archive on ${server}`, Archive];
    case "files.upload":
      return [`uploaded a file to ${server}`, FileText];
    case "files.upload.cancel":
      return [`cancelled an upload to ${server}`, FileText];
    case "backup.create":
      return [`backed up ${server}`, Archive];
    case "backup.restore":
      return [`restored a backup of ${server}`, Archive];
    case "backup.extract":
      return [`pulled files from a backup of ${server}`, Archive];
    case "backup.delete":
      return [`deleted a backup of ${server}`, Trash2];
    case "backup.lock":
      return [`locked or unlocked a backup of ${server}`, Archive];
    case "schedule.create":
      return [`made a schedule on ${server}`, CalendarClock];
    case "schedule.update":
      return [`changed a schedule on ${server}`, CalendarClock];
    case "schedule.delete":
      return [`deleted a schedule on ${server}`, CalendarClock];
    case "schedule.run":
      return [`ran a schedule on ${server}`, CalendarClock];
    case "sftp.disconnect":
      return [`ended their SFTP sessions on ${server}`, FolderKey];
    case "node.update":
      return [`updated Wings on ${server}`, Server];
    case "keys.add":
    case "keys.remove":
    case "keys.pair":
      return [`changed the trusted passkeys on ${server}`, KeyRound];
    case "node.sftp":
      return [`turned SFTP ${meta.enabled === false ? "off" : "on"} on ${server}`, FolderKey];
  }
  return [`ran ${action} on ${server}`, Activity];
}

// OrgActivity is the org's audit log or, given a server, the commands sent
// to it, as sentences about who did what. Admins and owners.
export function OrgActivity({
  orgId,
  nodeId = "",
  serverId = "",
}: {
  orgId: string;
  nodeId?: string;
  serverId?: string;
}) {
  // Names for the IDs the log has.
  const members = useQuery(OrgService.method.listMembers, { orgId });
  const nodes = useOrgNodes(orgId);
  const { servers } = useOrgServers(orgId);
  // A person by email: their name if they gave one.
  const person = (email: string) => {
    const m = members.data?.members.find((x) => x.email === email);
    return m?.name || email;
  };
  // Who an event was about: its subject, or the member its target names.
  const subject = (e: AuditEvent) => {
    if (e.subjectEmail) return person(e.subjectEmail);
    const m = members.data?.members.find((x) => x.userId === e.target);
    return m ? m.name || m.email : "someone who left";
  };
  const nodeName = (id: string) =>
    nodes.data?.nodes.find((n) => n.id === id)?.name ?? "a node that was removed";
  const serverName = (node: string, id: string) => {
    if (serverId) return "the server";
    const s = servers.find((x) => x.id === id && (!node || x.node.id === node));
    return s ? s.name : "a server";
  };

  const fetchPage = async (token: string): Promise<TimelinePage<AuditEvent>> => {
    const res = await orgClient.listAuditLog({ orgId, nodeId, serverId, pageToken: token });
    return { events: res.events, next: res.nextPageToken };
  };

  function toItem(e: AuditEvent): TimelineItem {
    const meta = JSON.parse(e.metadataJson || "{}") as Meta;
    const role = roleWords[str(meta.role)] ?? str(meta.role);
    let text: string;
    let icon: LucideIcon = Activity;
    let failed = false;
    switch (e.action) {
      case "org.create":
        [text, icon] = [`created the org${meta.name ? ` "${str(meta.name)}"` : ""}`, Building2];
        break;
      case "org.rename":
        [text, icon] = [`renamed the org to "${str(meta.name)}"`, Building2];
        break;
      case "member.role":
        [text, icon] = [`made ${subject(e)} ${role}`, Users];
        break;
      case "member.remove":
        [text, icon] = [`removed ${subject(e)} from the org`, UserMinus];
        break;
      case "member.leave":
        [text, icon] = ["left the org", LogOut];
        break;
      case "account.delete":
        [text, icon] = ["deleted their account and left the org", UserMinus];
        break;
      case "invitation.create":
        [text, icon] = [`invited ${str(meta.email)} as ${role}`, Mail];
        break;
      case "invitation.revoke":
        [text, icon] = ["cancelled an invitation", Mail];
        break;
      case "invitation.accept":
        [text, icon] = [`joined the org as ${role}`, UserPlus];
        break;
      case "access.set": {
        const n = Array.isArray(meta.permissions) ? meta.permissions.length : 0;
        [text, icon] = [
          `gave ${subject(e)} ${n} ${n === 1 ? "permission" : "permissions"} on ${serverName(str(meta.node), e.target)}`,
          KeyRound,
        ];
        break;
      }
      case "access.remove":
        [text, icon] = [
          `took away ${subject(e)}'s access to ${serverName(str(meta.node), e.target)}`,
          KeyRound,
        ];
        break;
      case "join_token.create":
        [text, icon] = ["made a join token to connect a node", Server];
        break;
      case "join_token.pin":
        [text, icon] = [`pinned the passkey "${str(meta.passkey)}" to a join token`, KeyRound];
        break;
      case "node.rename":
        [text, icon] = [`renamed a node to "${str(meta.name)}"`, Server];
        break;
      case "node.remove":
        [text, icon] = [`removed ${nodeName(e.target)}`, Trash2];
        break;
      case "node.sftp":
        [text, icon] = [
          `${meta.allowed === false ? "stopped" : "allowed"} SFTP on ${nodeName(e.target)}`,
          FolderKey,
        ];
        break;
      case "sftp.password":
        [text, icon] = [`turned on SFTP for ${serverName(str(meta.node), e.target)}`, FolderKey];
        break;
      case "backup_storage.enable":
        [text, icon] = [`turned on Raptor Backup Storage for ${nodeName(e.target)}`, Archive];
        break;
      case "backup_storage.disable":
        [text, icon] = [`turned off Raptor Backup Storage for ${nodeName(e.target)}`, Archive];
        break;
      case "egg.import":
        [text, icon] = [`imported the egg "${str(meta.name)}"`, Egg];
        break;
      case "egg.delete":
        [text, icon] = [`removed the imported egg "${str(meta.name)}"`, Egg];
        break;
      case "command": {
        const server =
          e.target === str(meta.node) ? nodeName(e.target) : serverName(str(meta.node), e.target);
        [text, icon] = describeCommand(str(meta.action), server, meta);
        if (meta.error) {
          text += ", which failed";
          failed = true;
        }
        break;
      }
      default: {
        const phrase = e.action.replace(/[._]/g, " ");
        text = e.target ? `${phrase}: ${e.target}` : phrase;
      }
    }
    return {
      id: e.id,
      at: e.at ? timestampDate(e.at) : undefined,
      icon,
      actor: e.actorEmail ? person(e.actorEmail) : str(meta.email) || "Someone who left",
      text,
      failed,
      details: [failed ? str(meta.error) : "", e.userAgent && describeDevice(e.userAgent), e.ip],
    };
  }

  return (
    <ActivityCard
      description={
        serverId
          ? "Everything done to this server: power, console commands, files, backups, and settings."
          : "Everything done in this org: members, invitations, nodes, access, and servers."
      }
      queryKey={["org-activity", orgId, nodeId, serverId]}
      fetchPage={fetchPage}
      toItem={toItem}
    />
  );
}
