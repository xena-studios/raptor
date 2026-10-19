import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import {
  Activity,
  Archive,
  Building2,
  CalendarClock,
  Clock,
  Database,
  Egg,
  FileText,
  FolderKey,
  HardDrive,
  KeyRound,
  LogOut,
  type LucideIcon,
  Mail,
  Network,
  Power,
  Puzzle,
  RefreshCw,
  Server,
  Settings,
  Shield,
  SquareTerminal,
  Trash2,
  UserMinus,
  UserPlus,
  Users,
} from "lucide-react";

import type { ReactNode } from "react";
import { describeDevice } from "@/components/account/security";
import {
  ActivityCard,
  type FilterOptions,
  type Filters,
  type TimelineItem,
  type TimelinePage,
  toFilter,
} from "@/components/activity-timeline";
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
      return [
        meta.permanent
          ? `deleted files for good on ${server}`
          : `moved files to the trash on ${server}`,
        Trash2,
      ];
    case "files.trash.restore":
      return [`restored files from the trash on ${server}`, RefreshCw];
    case "files.trash.delete":
      return [`deleted files from the trash for good on ${server}`, Trash2];
    case "files.trash.empty":
      return [`emptied the trash on ${server}`, Trash2];
    case "files.chmod":
      return [`changed file permissions on ${server}`, FileText];
    case "files.compress":
      return [`compressed files on ${server}`, Archive];
    case "files.decompress":
      return [`extracted an archive on ${server}`, Archive];
    case "files.upload":
      return [`uploaded a file to ${server}`, FileText];
    case "files.pull":
      return [`downloaded a file from a link onto ${server}`, FileText];
    case "files.import":
      return [`imported files onto ${server} from another host`, FileText];
    case "files.import.cancel":
      return [`stopped an import onto ${server}`, FileText];
    case "files.pull.cancel":
      return [`cancelled a download onto ${server}`, FileText];
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
    case "backup.policy.update":
      return [`changed where ${server}'s backups go`, Archive];
    case "backup.destination.save":
      return [`saved a backup destination on ${server}`, Archive];
    case "backup.destination.delete":
      return [`removed a backup destination from ${server}`, Trash2];
    case "backup.destination.test":
      return [`tested a backup destination on ${server}`, Archive];
    case "backup.key.mode":
      return [`changed who keeps ${server}'s backup key`, KeyRound];
    case "backup.key.show":
      return [`viewed ${server}'s backup key`, KeyRound];
    case "backup.recover":
      return [`recovered backups onto ${server}`, Archive];
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
    case "support.grant":
      return [`gave Raptor support access to ${server}`, KeyRound];
    case "support.revoke":
      return [`ended Raptor support's access to ${server}`, KeyRound];
    case "support.disable":
      return [`turned support access ${meta.off === false ? "on" : "off"} on ${server}`, KeyRound];
    case "keys.add":
    case "keys.remove":
    case "keys.pair":
      return [`changed the trusted passkeys on ${server}`, KeyRound];
    case "node.sftp":
      return [`turned SFTP ${meta.enabled === false ? "off" : "on"} on ${server}`, FolderKey];
    case "server.move":
      return [`moved ${server} to another disk`, HardDrive];
    case "server.console":
      return [`used ${server}'s console`, SquareTerminal];
    case "addons.install":
      return [`installed an add-on on ${server}`, Puzzle];
    case "backup.key":
      return [`gave Raptor a copy of ${server}'s backup key`, KeyRound];
    case "node.disk.setup":
      return [`set up a disk on ${server}`, HardDrive];
    case "node.disk.grow":
      return [`grew a disk on ${server}`, HardDrive];
    case "node.disk.default":
      return [`changed the default disk on ${server}`, HardDrive];
    case "node.firewall":
      return [`changed the firewall on ${server}`, Shield];
    case "node.network.save":
      return [`saved a network on ${server}`, Network];
    case "node.network.delete":
      return [`deleted a network on ${server}`, Network];
    case "node.network.rules":
      return [`changed the rules between networks on ${server}`, Network];
    case "node.db.instance.save":
      return [`saved a database instance on ${server}`, Database];
    case "node.db.instance.power":
      return [`started or stopped a database instance on ${server}`, Database];
    case "node.db.instance.delete":
      return [`deleted a database instance on ${server}`, Database];
    case "node.db.instance.version":
      return [`changed a database instance's version on ${server}`, Database];
    case "node.db.instance.goback":
      return [`put a database instance on ${server} back to its version before`, Database];
    case "node.db.instance.external":
      return [`changed a database instance's external access on ${server}`, Database];
    case "db.restore":
      return [`restored one of ${server}'s databases from a backup`, Database];
    case "db.create":
      return [`made a database for ${server}`, Database];
    case "db.delete":
      return [`deleted one of ${server}'s databases`, Database];
    case "db.password":
      return [`looked at a password of ${server}'s databases`, KeyRound];
    case "db.password.reset":
      return [`gave one of ${server}'s databases a new password`, KeyRound];
    case "server.networks":
      return [`changed the networks ${server} is on`, Network];
    case "node.hostname":
      return [`renamed the machine ${server} runs on`, Server];
    case "node.time":
      return [`changed the clock settings on ${server}`, Clock];
    case "node.reboot":
      return [`restarted the machine ${server} runs on`, RefreshCw];
  }
  return [`made a change on ${server}`, Activity];
}

// The kinds of event the org's lists filter by: an org event's action, or a
// command's (ActivityFilter.actions).
const power = {
  label: "Power",
  actions: ["server.start", "server.stop", "server.restart", "server.kill"],
};
const consoleKind = { label: "Console", actions: ["server.command", "server.console"] };
const files = { label: "Files and SFTP", actions: ["files.%", "sftp.%"] };
const backups = { label: "Backups", actions: ["backup.%", "backup_storage.%"] };
const schedules = { label: "Schedules", actions: ["schedule.%"] };
const databases = { label: "Databases", actions: ["db.%", "node.db.%"] };
const serverChanges = {
  label: "Server changes",
  actions: [
    "server.create",
    "server.delete",
    "server.update",
    "server.reinstall",
    "server.move",
    "server.networks",
    "server.display_address",
    "addons.install",
  ],
};
const access = { label: "Access", actions: ["access.%"] };
const people = {
  label: "Members and invitations",
  actions: ["member.%", "invitation.%", "account.delete"],
};
const machine = { label: "Node and machine", actions: ["node.%", "join_token.%", "keys.%"] };
const support = { label: "Raptor support", actions: ["support.%"] };
const eggs = { label: "Eggs", actions: ["egg.%"] };
const orgKind = { label: "The org", actions: ["org.%"] };

const serverKinds = [
  power,
  consoleKind,
  files,
  backups,
  schedules,
  databases,
  serverChanges,
  access,
];
const nodeKinds = [
  machine,
  power,
  consoleKind,
  files,
  backups,
  schedules,
  databases,
  serverChanges,
  support,
];
const orgKinds = [
  power,
  consoleKind,
  files,
  backups,
  schedules,
  databases,
  serverChanges,
  access,
  people,
  machine,
  support,
  eggs,
  orgKind,
];

// OrgActivity is the org's audit log or, given a server, the commands sent
// to it, as sentences about who did what. Admins and owners.
export function OrgActivity({
  orgId,
  nodeId = "",
  serverId = "",
  limit,
  action,
}: {
  orgId: string;
  nodeId?: string;
  serverId?: string;
  // The newest few only, for the org's overview, with action beside the title.
  limit?: number;
  action?: ReactNode;
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

  // What the list can be narrowed by: not the overview's few.
  const filters: FilterOptions | undefined = limit
    ? undefined
    : {
        kinds: serverId ? serverKinds : nodeId ? nodeKinds : orgKinds,
        people: (members.data?.members ?? []).map((m) => ({
          id: m.userId,
          label: m.name || m.email,
        })),
        // The org's whole log can be narrowed to a node or a server.
        places:
          nodeId || serverId
            ? undefined
            : {
                label: "Where",
                options: [
                  ...(nodes.data?.nodes ?? []).map((n) => ({
                    value: `node:${n.id}`,
                    label: `${n.name} (node)`,
                  })),
                  ...servers.map((x) => ({
                    value: `server:${x.node.id}:${x.id}`,
                    label: x.name,
                  })),
                ],
              },
        failures: true,
      };

  const fetchPage = async (token: string, f: Filters): Promise<TimelinePage<AuditEvent>> => {
    // A place picked in the org's log narrows it the way a node's or a
    // server's own tab does.
    const [kind, a = "", b = ""] = f.place.split(":");
    const scope =
      kind === "node"
        ? { nodeId: a, serverId: "" }
        : kind === "server"
          ? { nodeId: a, serverId: b }
          : { nodeId, serverId };
    const res = await orgClient.listAuditLog({
      orgId,
      ...scope,
      pageToken: token,
      filter: toFilter(f, filters),
    });
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
      case "org.delete":
        [text, icon] = ["deleted the org", Trash2];
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
      case "node.display_address":
        [text, icon] = [
          meta.address
            ? `set ${nodeName(e.target)}'s shown address to ${str(meta.address)}`
            : `cleared ${nodeName(e.target)}'s shown address`,
          Server,
        ];
        break;
      case "server.display_address":
        [text, icon] = [
          meta.address
            ? `set ${serverName(str(meta.node), e.target)}'s shown address to ${str(meta.address)}`
            : `cleared ${serverName(str(meta.node), e.target)}'s shown address`,
          Server,
        ];
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
      case "support.request":
        [text, icon] = [
          `(Raptor support) asked for access to ${nodeName(str(meta.node))}: ${str(meta.reason)} (${str(meta.ticket)})`,
          KeyRound,
        ];
        break;
      case "support.deny":
        [text, icon] = ["denied Raptor support's request for access", KeyRound];
        break;
      case "egg.import":
        [text, icon] = [`imported the egg "${str(meta.name)}"`, Egg];
        break;
      case "egg.delete":
        [text, icon] = [`removed the imported egg "${str(meta.name)}"`, Egg];
        break;
      case "egg.create":
        [text, icon] = [`made the egg "${str(meta.name) || "an egg"}"`, Egg];
        break;
      case "egg.update":
        [text, icon] = [`edited the egg${meta.name ? ` "${str(meta.name)}"` : ""}`, Egg];
        break;
      case "egg.customize":
        [text, icon] = [
          `made a copy of the egg${meta.name ? ` "${str(meta.name)}"` : ""} to customize`,
          Egg,
        ];
        break;
      case "backup.recover.prepare":
        [text, icon] = [`started recovering backups onto ${nodeName(e.target)}`, Archive];
        break;
      case "org.suspend":
        [text, icon] = ["(Raptor staff) suspended the org", Building2];
        break;
      case "org.unsuspend":
        [text, icon] = ["(Raptor staff) lifted the org's suspension", Building2];
        break;
      case "command": {
        const onNode = e.target === str(meta.node);
        // A server command aimed at the node is one whose server doesn't
        // exist yet (or any more), like a create that failed.
        const server = !onNode
          ? serverName(str(meta.node), e.target)
          : str(meta.action).startsWith("server.")
            ? `a server on ${nodeName(e.target)}`
            : nodeName(e.target);
        [text, icon] = describeCommand(str(meta.action), server, meta);
        if (meta.error) {
          text += ", which failed";
          failed = true;
        }
        break;
      }
      default:
        // Something this version of the app has no words for yet: say that
        // much, never the raw action or an ID.
        text = "made a change";
    }
    return {
      id: e.id,
      at: e.at ? timestampDate(e.at) : undefined,
      icon,
      actor: e.actorEmail ? person(e.actorEmail) : str(meta.email) || "Someone who left",
      actorId: members.data?.members.find((m) => m.email === e.actorEmail)?.userId,
      text,
      failed,
      // A failure's reason is the node's words, not written for people, so
      // only the fact that it failed is shown.
      details: [e.userAgent && describeDevice(e.userAgent)],
      ip: e.ip,
    };
  }

  return (
    <ActivityCard
      title={limit ? "Recent activity" : undefined}
      limit={limit}
      action={action}
      refetchInterval={limit ? 30_000 : undefined}
      description={
        limit
          ? "The latest in this org."
          : serverId
            ? "Everything done to this server: power, console commands, files, backups, and settings."
            : nodeId
              ? "Everything done to this node and the servers on it, kept for a year."
              : "Everything done in this org: members, invitations, nodes, access, and servers. Kept for a year."
      }
      queryKey={["org-activity", orgId, nodeId, serverId]}
      fetchPage={fetchPage}
      toItem={toItem}
      filters={filters}
    />
  );
}
