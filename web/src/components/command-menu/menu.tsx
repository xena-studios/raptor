import { Autocomplete } from "@base-ui/react/autocomplete";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { cn } from "cn";
import {
  Archive,
  BookOpen,
  Building2,
  CalendarClock,
  ChevronRight,
  Clipboard,
  CornerDownLeft,
  Crown,
  Egg,
  ExternalLink,
  FolderOpen,
  Gamepad2,
  HelpCircle,
  type LucideIcon,
  Mail,
  PackageCheck,
  PanelTop,
  Pin,
  Play,
  Plus,
  Radar,
  RotateCw,
  Search,
  Server,
  Share2,
  Shield,
  Skull,
  Square,
  SquareTerminal,
  Terminal,
  UserMinus,
  UserPlus,
  UserRound,
  Users,
} from "lucide-react";
import { type Dispatch, type SetStateAction, useMemo, useRef } from "react";

import { useConfirm } from "@/components/confirm";
import { toast } from "@/components/ui/toast";
import { EggService } from "@/gen/raptor/panel/v1/egg_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { StaffService } from "@/gen/raptor/panel/v1/staff_pb";
import { useAddonSections } from "@/lib/addon-sections";
import { nodeHostname, serverAddress } from "@/lib/addresses";
import { leaveIntent } from "@/lib/command-intent";
import {
  actDef,
  type CommandItem,
  type CommandRun,
  contextOf,
  docsItem,
  type EggInfo,
  eggItem,
  eggItems,
  filesSearch,
  type GroupKey,
  groupNames,
  type IconKey,
  inviteCheck,
  memberItem,
  type NodeInfo,
  nodeItem,
  nodeItems,
  type OrgInfo,
  orgActions,
  pageItems,
  pickable,
  pickItems,
  type ServerAct,
  type ServerExtras,
  type ServerInfo,
  serverItem,
  serverItems,
  tabRoute,
} from "@/lib/command-items";
import { type Memory, recentIds } from "@/lib/command-recents";
import { rank } from "@/lib/command-search";
import {
  crumbs,
  current,
  isInput,
  onBackspace,
  placeholder,
  push,
  type Stack,
  type Step,
  setQuery,
} from "@/lib/command-steps";
import { addonKinds, categoryName } from "@/lib/eggs";
import { message } from "@/lib/errors";
import { isAdmin, roleNames } from "@/lib/format";
import { useGameTab } from "@/lib/game-tab";
import { discordUrl, docsUrl } from "@/lib/help";
import { sameId } from "@/lib/ids";
import { useMinecraftServer } from "@/lib/minecraft-tab";
import { type OrgServer, useOrgNodes, useOrgServers } from "@/lib/org-data";
import { usePlayersTab } from "@/lib/players-tab";
import { type ServerConfig, serverStatus, webLink } from "@/lib/servers";
import { commandClient, orgClient } from "@/lib/transport";

import { Keys } from "./keys";

const icons: Record<IconKey, LucideIcon> = {
  page: PanelTop,
  server: Gamepad2,
  node: Server,
  egg: Egg,
  member: Users,
  docs: BookOpen,
  help: HelpCircle,
  plus: Plus,
  invite: Mail,
  org: Building2,
  staff: Shield,
  account: UserRound,
  tab: ChevronRight,
  start: Play,
  restart: RotateCw,
  stop: Square,
  kill: Skull,
  command: Terminal,
  console: SquareTerminal,
  backup: Archive,
  schedule: CalendarClock,
  addons: PackageCheck,
  whitelist: UserPlus,
  op: Crown,
  kick: UserMinus,
  files: FolderOpen,
  address: Clipboard,
  dashboard: ExternalLink,
  "share-log": Share2,
  "test-connection": Radar,
};

type Group = { value: string; label: string; items: CommandItem[] };

type BodyProps = {
  orgId?: string;
  stack: Stack;
  setStack: Dispatch<SetStateAction<Stack>>;
  memory: Memory;
  remember: (id: string) => void;
  togglePin: (id: string) => void;
  close: () => void;
  openHelp: () => void;
};

// MenuBody is the menu's search, results, and steps. It's mounted only
// while the menu is open, so its queries run then (most are the pages'
// own, already cached).
export function MenuBody(props: BodyProps) {
  return props.orgId ? <OrgMenu {...props} orgId={props.orgId} /> : <Menu {...props} />;
}

// OrgData is what the menu knows about the org: its servers and nodes,
// eggs, members, and what the server you're on knows of itself.
type OrgData = {
  servers: OrgServer[];
  infos: ServerInfo[];
  nodes: NodeInfo[];
  eggs: EggInfo[];
  members: { userId: string; email: string; name: string; role: string }[];
  context?:
    | { kind: "server"; id: string; extras: ServerExtras; firstKind?: string }
    | { kind: "node"; id: string };
};

function infoOf(s: OrgServer): ServerInfo {
  return {
    id: s.id,
    name: s.name || s.id.slice(-8),
    nodeId: s.node.id,
    nodeName: s.node.name,
    nodeConnected: s.node.connected,
    egg: s.eggName,
    address: serverAddress(s.node, s, s.ports[0]).text,
    state: s.state,
    installState: s.installState,
    status: serverStatus(s, s.node.connected).label,
    permissions: s.permissions,
    playerLists: s.playerLists,
    rconPlayers: s.rconPlayers,
  };
}

function OrgMenu(props: BodyProps & { orgId: string }) {
  const { orgId } = props;
  const path = useLocation({ select: (l) => l.pathname });
  const { servers } = useOrgServers(orgId, 15_000);
  const nodes = useOrgNodes(orgId);
  const eggs = useQuery(EggService.method.listEggs, { orgId }, { staleTime: 60_000 });
  const members = useQuery(OrgService.method.listMembers, { orgId }, { staleTime: 60_000 });
  const ctx = contextOf(path);
  const ctxServer = ctx?.kind === "server" ? servers.find((s) => sameId(s.id, ctx.ref)) : undefined;
  const extras = useServerExtras(orgId, ctxServer);

  const data = useMemo<OrgData>(() => {
    const nodeList = nodes.data?.nodes ?? [];
    const ctxNode = ctx?.kind === "node" ? nodeList.find((n) => sameId(n.id, ctx.ref)) : undefined;
    return {
      servers,
      infos: servers.map(infoOf),
      nodes: nodeList.map((n) => ({
        id: n.id,
        name: n.name,
        hostname: nodeHostname(n),
        connected: n.connected,
      })),
      eggs: (eggs.data?.eggs ?? []).map((e) => ({
        id: e.id,
        name: e.name,
        category: e.category,
        categoryName: categoryName(e.category),
      })),
      members: (members.data?.members ?? []).map((m) => ({
        userId: m.userId,
        email: m.email,
        name: m.name,
        role: roleNames[m.role],
      })),
      context: ctxServer
        ? { kind: "server", id: ctxServer.id, extras: extras.extras, firstKind: extras.firstKind }
        : ctxNode
          ? { kind: "node", id: ctxNode.id }
          : undefined,
    };
  }, [servers, nodes.data, eggs.data, members.data, ctxServer, ctx?.kind, ctx?.ref, extras]);

  return <Menu {...props} data={data} />;
}

// useServerExtras is what the server you're on knows from its config, by
// the same hooks its page uses for its tabs (so they agree).
function useServerExtras(orgId: string, s: OrgServer | undefined) {
  const nodeId = s?.node.id ?? "";
  const serverId = s?.id ?? "";
  const { sections } = useAddonSections(orgId, nodeId, serverId, s, s?.node.capabilities);
  const game = useGameTab(orgId, nodeId, serverId, s);
  const players = usePlayersTab(orgId, nodeId, serverId, s);
  const worlds = useMinecraftServer(orgId, nodeId, serverId, s);
  const details = useQuery(
    OrgService.method.getServer,
    { orgId, nodeId, serverId },
    { enabled: !!s },
  );
  const configJson = details.data?.configJson;
  return useMemo(() => {
    const cfg = configJson ? (JSON.parse(configJson) as ServerConfig) : undefined;
    const host = s ? s.displayAddress || nodeHostname(s.node) : "";
    const link = cfg && host ? webLink(cfg, host) : undefined;
    // An add-on kind with an installer: what "Update all add-ons" updates.
    const firstKind = sections.find((x) => addonKinds[x.key])?.key;
    const extras: ServerExtras = {
      game,
      addons: sections.length > 0,
      addonKinds: !!firstKind,
      players,
      worlds,
      dashboard: link?.reachable ? { name: link.name, url: link.url } : undefined,
    };
    return { extras, firstKind };
  }, [configJson, s, sections, game, players, worlds]);
}

const emptyData: OrgData = { servers: [], infos: [], nodes: [], eggs: [], members: [] };

// Caps on how many of a group show: a few each while searching (the best
// first), more when a group is all there is.
const capEmpty: Partial<Record<GroupKey, number>> = {
  pinned: 12,
  context: 12,
  recent: 6,
  actions: 6,
  servers: 6,
  nodes: 4,
  pages: 8,
};
const capSearch = 6;
const capAlone = 40;

function Menu({
  orgId,
  stack,
  setStack,
  memory,
  remember,
  togglePin,
  close,
  openHelp,
  data = emptyData,
}: BodyProps & { data?: OrgData }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { confirm } = useConfirm();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const staff = useQuery(StaffService.method.getStaffMe, {}, { staleTime: 5 * 60_000 });
  const frame = current(stack);
  const { step, query } = frame;
  const highlighted = useRef<CommandItem | undefined>(undefined);

  const orgList: OrgInfo[] = (orgs.data?.orgs ?? []).map((o) => ({
    id: o.id,
    name: o.name,
    admin: isAdmin(o.role),
  }));
  const org = orgList.find((o) => o.id === orgId);
  const info = (id: string) => data.infos.find((s) => s.id === id);
  const ctxId = data.context?.id;

  // Every item the root offers, built once per change of data.
  const all = useMemo(() => {
    const out: CommandItem[] = [];
    if (!orgId) return out;
    const ctx = data.context;
    out.push(...orgActions(org), ...pickItems(data.infos));
    for (const s of data.infos) {
      const here = ctx?.kind === "server" && ctx.id === s.id;
      out.push(serverItem(orgId, s, here ? 8 : undefined));
      out.push(
        ...(here
          ? serverItems(orgId, s, { extras: ctx.extras, group: "context", boost: 30 })
          : serverItems(orgId, s, { searchOnly: true })),
      );
    }
    for (const n of data.nodes) {
      const here = ctx?.kind === "node" && ctx.id === n.id;
      out.push(nodeItem(orgId, n, here ? 8 : undefined));
      if (org) {
        out.push(
          ...(here
            ? nodeItems(org, n, { group: "context", boost: 30 })
            : nodeItems(org, n, { searchOnly: true })),
        );
      }
    }
    for (const e of data.eggs) out.push({ ...eggItem(orgId, e), searchOnly: true });
    for (const m of data.members) out.push({ ...memberItem(orgId, m), searchOnly: true });
    return out;
  }, [orgId, org, data]);
  const pages = useMemo(
    () => pageItems({ org, orgs: orgList, staff: !!staff.data?.role, docsUrl, discordUrl }),
    [org, orgList, staff.data?.role],
  );

  const uses = useMemo(() => new Map(Object.entries(memory.uses)), [memory.uses]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: info only reads data
  const groups = useMemo<Group[]>(() => {
    const everything = [...all, ...pages];
    const byId = new Map(everything.map((i) => [i.id, i]));
    const ctxLabel =
      data.context?.kind === "server"
        ? info(data.context.id)?.name
        : data.context?.kind === "node"
          ? data.nodes.find((n) => n.id === data.context?.id)?.name
          : undefined;
    const label = (g: GroupKey) => (g === "context" && ctxLabel ? ctxLabel : groupNames[g]);
    const list = (key: string, name: string, items: CommandItem[]): Group => ({
      value: key,
      label: name,
      items,
    });

    switch (step.kind) {
      case "root": {
        if (!query.trim()) {
          const shown = new Set<string>();
          const out: Group[] = [];
          const add = (g: GroupKey, items: CommandItem[]) => {
            const fresh = items.filter((i) => !shown.has(i.id)).slice(0, capEmpty[g] ?? capSearch);
            for (const i of fresh) shown.add(i.id);
            if (fresh.length > 0) out.push(list(g, label(g), fresh));
          };
          const lookup = (ids: string[]) =>
            ids.map((id) => byId.get(id)).filter((i): i is CommandItem => !!i);
          add("pinned", lookup(memory.pinned));
          add(
            "context",
            all.filter((i) => i.group === "context"),
          );
          add("recent", lookup(recentIds(memory, 12)));
          for (const g of ["actions", "servers", "nodes", "pages"] as GroupKey[]) {
            const items = everything.filter((i) => i.group === g && !i.searchOnly);
            add(
              g,
              rank(items, "", uses).map((r) => r.item),
            );
          }
          return out;
        }
        const ranked = rank(everything, query, uses);
        const byGroup = new Map<GroupKey, { items: CommandItem[]; best: number }>();
        for (const { item, score } of ranked) {
          const g = byGroup.get(item.group) ?? { items: [], best: score };
          g.items.push(item);
          byGroup.set(item.group, g);
        }
        const alone = byGroup.size <= 1;
        const out = [...byGroup.entries()]
          .sort((a, b) => b[1].best - a[1].best)
          .map(([g, v]) =>
            list(g, label(g), v.items.slice(0, alone ? capAlone : g === "context" ? 8 : capSearch)),
          );
        out.push(list("docs", groupNames.docs, [docsItem(docsUrl, query)]));
        return out;
      }
      case "pick": {
        const d = actDef(step.act);
        const items = pickable(step.act, data.infos).map((s) => ({
          ...serverItem(orgId ?? "", s, s.id === ctxId ? 20 : undefined),
          id: `act:${step.act}:${s.id}`,
          label: s.name,
          run: { kind: "act", act: step.act, serverId: s.id } as CommandRun,
          drill: undefined,
          destructive: d.confirm?.destructive,
        }));
        const ranked = rank(items, query, uses).map((r) => r.item);
        return [list("servers", groupNames.servers, ranked.slice(0, capAlone))];
      }
      case "server": {
        const s = info(step.serverId);
        if (!s || !orgId) return [];
        const extras =
          ctxId === s.id && data.context?.kind === "server" ? data.context.extras : undefined;
        const items = serverItems(orgId, s, { extras, own: true });
        const ranked = rank(items, query, uses).map((r) => r.item);
        return [list("server", s.name, ranked)];
      }
      case "node": {
        const n = data.nodes.find((x) => x.id === step.nodeId);
        if (!n || !org) return [];
        const own = rank(nodeItems(org, n, { own: true }), query, uses).map((r) => r.item);
        const on = rank(
          data.infos.filter((s) => s.nodeId === n.id).map((s) => serverItem(org.id, s)),
          query,
          uses,
        ).map((r) => r.item);
        return [list("node", n.name, own), list("servers", groupNames.servers, on)].filter(
          (g) => g.items.length > 0,
        );
      }
      case "egg": {
        const e = data.eggs.find((x) => x.id === step.eggId);
        if (!e || !org) return [];
        return [
          list(
            "egg",
            e.name,
            rank(eggItems(org, e), query, uses).map((r) => r.item),
          ),
        ];
      }
      case "schedule":
        return [];
      case "input": {
        const s = info(step.serverId);
        const d = actDef(step.act);
        if (!s || !d.input) return [];
        const problem = query.trim() ? d.input.check(query.trim()) : undefined;
        return [
          list("input", d.short.replace(/…$/, ""), [
            {
              id: "submit",
              label: d.input.submit(query.trim(), s.name),
              hint: problem,
              group: "actions",
              icon: step.act,
              run: { kind: "submit" },
              destructive: d.confirm?.destructive,
            },
          ]),
        ];
      }
      case "invite": {
        const e = query.trim();
        return [
          list("input", "Invite a member", [
            {
              id: "submit",
              label: e ? `Invite ${e}` : "Type their email address",
              hint: e ? inviteCheck(e) : undefined,
              group: "actions",
              icon: "invite",
              run: { kind: "submit" },
            },
          ]),
        ];
      }
    }
  }, [step, query, all, pages, memory, uses, data, orgId, org, ctxId]);

  // A schedule step lists the server's schedules.
  const schedServer = step.kind === "schedule" ? info(step.serverId) : undefined;
  const schedules = useQuery(
    OrgService.method.listSchedules,
    { orgId: orgId ?? "", nodeId: schedServer?.nodeId ?? "", serverId: schedServer?.id ?? "" },
    { enabled: !!schedServer && !!orgId },
  );
  const shownGroups = useMemo<Group[]>(() => {
    if (step.kind !== "schedule" || !schedServer) return groups;
    const items: CommandItem[] = (schedules.data?.schedules ?? []).map((sc) => ({
      id: `schedule:${schedServer.id}:${sc.id}`,
      label: sc.name || "Unnamed schedule",
      hint: sc.enabled ? undefined : "Off (runs only by hand)",
      group: "actions",
      icon: "schedule",
      run: { kind: "run-schedule", serverId: schedServer.id, scheduleId: sc.id, name: sc.name },
    }));
    return [
      list2(
        schedServer.name,
        rank(items, query).map((r) => r.item),
      ),
    ];
  }, [step.kind, schedServer, schedules.data, groups, query]);

  const count = shownGroups.reduce((n, g) => n + g.items.length, 0);
  const nameOf = (id: string) => info(id)?.name ?? "Server";
  const trail = crumbs(stack, {
    server: nameOf,
    node: (id) => data.nodes.find((n) => n.id === id)?.name ?? "Node",
    egg: (id) => data.eggs.find((e) => e.id === id)?.name ?? "Egg",
    verb: (a) => actDef(a).verb,
    short: (a) => actDef(a).short,
  });
  const inputAct = step.kind === "input" ? actDef(step.act) : undefined;

  function go(s: Step, q = "") {
    setStack((st) => push(st, s, q));
  }

  // fail says what went wrong in a toast (the menu has closed by then).
  function fail(what: string, err: unknown) {
    toast.error(what, message(err));
  }

  async function command(s: ServerInfo, action: string, params: object, done?: string) {
    try {
      await commandClient.execute({
        nodeId: s.nodeId,
        serverId: s.id,
        action,
        paramsJson: JSON.stringify(params),
      });
      if (done) toast.success(done);
      await queryClient.invalidateQueries();
    } catch (err) {
      fail(`Couldn't do that on ${s.name}`, err);
    }
  }

  async function copy(text: string, what: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast.success(`Copied the ${what}`);
    } catch (err) {
      fail(`Couldn't copy the ${what}`, err);
    }
  }

  function goto(to: string, params?: Record<string, string>, search?: Record<string, string>) {
    void navigate({ to, params, search } as never);
  }

  async function act(act: ServerAct, serverId: string) {
    const s = info(serverId);
    if (!s || !orgId) return;
    const d = actDef(act);
    const params = { orgId, serverId };
    if (d.input) {
      go({ kind: "input", act, serverId });
      return;
    }
    if (act === "schedule") {
      go({ kind: "schedule", serverId });
      return;
    }
    close();
    switch (act) {
      case "start":
      case "restart":
        await command(s, `server.${act}`, {});
        return;
      case "stop":
      case "kill": {
        const c = d.confirm;
        if (!c) return;
        const ok = await confirm({
          title: c.title(s.name),
          description: c.description,
          confirm: c.confirm,
          destructive: c.destructive,
        });
        if (ok) await command(s, `server.${act}`, {});
        return;
      }
      case "backup":
        await command(s, "backup.create", {});
        return;
      case "addons": {
        let kind =
          data.context?.kind === "server" && data.context.id === s.id
            ? data.context.firstKind
            : undefined;
        if (!kind) {
          try {
            const res = await orgClient.getServer({ orgId, nodeId: s.nodeId, serverId: s.id });
            const cfg = res.configJson ? (JSON.parse(res.configJson) as ServerConfig) : undefined;
            kind = Object.keys(cfg?.egg?.addons ?? {}).find((k) => addonKinds[k]);
          } catch (err) {
            fail(`Couldn't look at ${s.name}'s add-ons`, err);
            return;
          }
        }
        if (!kind) {
          toast.error(
            `${s.name} has no add-ons to update`,
            "Its egg has no plugin or mod manager.",
          );
          return;
        }
        leaveIntent({ kind: "addons.update", serverId: s.id });
        goto("/orgs/$orgId/servers/$serverId/addons/$kind", { ...params, kind });
        return;
      }
      case "address":
        await copy(s.address, `address of ${s.name}`);
        return;
      case "share-log":
        leaveIntent({ kind: "log.share", serverId: s.id });
        goto(tabRoute("overview"), params);
        return;
      case "test-connection":
        leaveIntent({ kind: "connection.test", serverId: s.id });
        goto(tabRoute("network"), params);
        return;
    }
  }

  async function submit() {
    const value = query.trim();
    if (step.kind === "invite") {
      if (!orgId || inviteCheck(value)) return;
      close();
      leaveIntent({ kind: "member.invite", orgId, email: value });
      goto("/orgs/$orgId/settings/members", { orgId });
      return;
    }
    if (step.kind !== "input") return;
    const s = info(step.serverId);
    const d = actDef(step.act);
    if (!s || !orgId || !d.input || d.input.check(value)) return;
    const params = { orgId, serverId: s.id };
    close();
    switch (step.act) {
      case "command":
        await command(s, "server.command", { command: value }, `Sent to ${s.name}`);
        return;
      case "whitelist":
      case "op":
      case "kick":
        leaveIntent({ kind: "players", serverId: s.id, op: step.act, name: value });
        goto(tabRoute("players"), params);
        return;
      case "files":
        goto(tabRoute("files"), params, filesSearch(value));
        return;
    }
  }

  async function run(item: CommandItem) {
    const r = item.run;
    if (r.kind !== "submit" && r.kind !== "run-schedule" && item.group !== "docs")
      remember(item.id);
    switch (r.kind) {
      case "go":
        close();
        goto(r.to, r.params, r.search);
        return;
      case "open":
        close();
        window.open(r.url, "_blank", "noopener");
        return;
      case "act":
        await act(r.act, r.serverId);
        return;
      case "pick":
        go({ kind: "pick", act: r.act });
        return;
      case "server":
        go({ kind: "server", serverId: r.serverId });
        return;
      case "node":
        go({ kind: "node", nodeId: r.nodeId });
        return;
      case "egg":
        go({ kind: "egg", eggId: r.eggId });
        return;
      case "copy":
        close();
        await copy(r.text, r.what);
        return;
      case "invite":
        go({ kind: "invite" });
        return;
      case "help":
        openHelp();
        return;
      case "run-schedule": {
        const s = info(r.serverId);
        if (!s) return;
        close();
        await command(s, "schedule.run", { schedule_id: r.scheduleId });
        return;
      }
      case "submit":
        await submit();
        return;
    }
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    const h = highlighted.current;
    if (e.key === "Backspace") {
      const prev = onBackspace(stack, query);
      if (prev) {
        e.preventDefault();
        setStack(prev);
      }
      return;
    }
    if (e.key === "Tab" && !e.shiftKey && h?.drill && !isInput(step)) {
      e.preventDefault();
      void run({ ...h, run: h.drill });
      return;
    }
    if (e.altKey && e.code === "KeyP" && h && !isInput(step) && h.id !== "submit") {
      e.preventDefault();
      togglePin(h.id);
      return;
    }
    if (e.key === "?" && query === "" && !isInput(step)) {
      e.preventDefault();
      openHelp();
    }
  }

  const status =
    step.kind === "schedule" && schedules.isPending
      ? "Loading schedules…"
      : isInput(step)
        ? (shownGroups[0]?.items[0]?.hint ?? "")
        : count === 0
          ? "No results"
          : `${count} result${count === 1 ? "" : "s"}`;

  return (
    <Autocomplete.Root
      open
      inline
      mode="none"
      items={shownGroups}
      itemToStringValue={(i: CommandItem) => i.label}
      value={query}
      onValueChange={(v, d) => {
        if (d.reason === "input-change" || d.reason === "input-clear") {
          setStack((s) => setQuery(s, v));
        }
      }}
      onItemHighlighted={(v: CommandItem | undefined) => {
        highlighted.current = v;
      }}
      autoHighlight="always"
      keepHighlight
    >
      <div className="flex flex-col gap-1.5 border-b px-3 pt-3 pb-2">
        {trail.length > 0 && (
          <nav aria-label="Steps" className="flex min-w-0 flex-wrap items-center gap-1 text-xs">
            <button
              type="button"
              className="font-mono text-muted-foreground uppercase hover:text-foreground"
              onClick={() => setStack((s) => s.slice(0, 1))}
            >
              Search
            </button>
            {trail.map((c, i) => (
              // biome-ignore lint/suspicious/noArrayIndexKey: a crumb is its place in the steps
              <span key={`${i}-${c}`} className="flex min-w-0 items-center gap-1">
                <ChevronRight className="size-3 shrink-0 text-muted-foreground" aria-hidden />
                {i < trail.length - 1 ? (
                  <button
                    type="button"
                    className="truncate text-muted-foreground hover:text-foreground"
                    onClick={() => setStack((s) => s.slice(0, i + 2))}
                  >
                    {c}
                  </button>
                ) : (
                  <span className="truncate font-medium" aria-current="step">
                    {c}
                  </span>
                )}
              </span>
            ))}
          </nav>
        )}
        <div className="flex items-center gap-2">
          {isInput(step) ? (
            <CornerDownLeft className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          ) : (
            <Search className="size-4 shrink-0 text-muted-foreground" aria-hidden />
          )}
          <Autocomplete.Input
            aria-label={
              isInput(step)
                ? (inputAct?.input?.label ?? "Email address")
                : "Search pages, servers, and actions"
            }
            aria-describedby="command-menu-keys"
            placeholder={placeholder(step, inputAct?.input?.placeholder)}
            autoComplete="off"
            spellCheck={false}
            maxLength={step.kind === "input" && step.act === "command" ? 4096 : 200}
            type={step.kind === "invite" ? "email" : "text"}
            className={cn(
              "h-9 w-full min-w-0 bg-transparent text-base outline-none placeholder:text-muted-foreground md:text-sm",
              step.kind === "input" && step.act === "command" && "font-mono",
            )}
            onKeyDown={onKeyDown}
          />
          <Keys k="Esc" className="hidden sm:inline-flex" />
        </div>
      </div>

      <Autocomplete.Status className="sr-only">{status}</Autocomplete.Status>

      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain scroll-py-1">
        <Autocomplete.Empty>
          <div className="px-4 py-8 text-center text-sm text-muted-foreground">
            {step.kind === "schedule"
              ? schedules.isPending
                ? "Loading schedules…"
                : "This server has no schedules yet."
              : step.kind === "pick"
                ? "No server you can do this to."
                : "Nothing matches. Try fewer letters."}
          </div>
        </Autocomplete.Empty>
        <Autocomplete.List className="py-1">
          {(group: Group) => (
            <Autocomplete.Group key={group.value} items={group.items} className="not-last:mb-1">
              <Autocomplete.GroupLabel className="px-3 pt-2 pb-1 font-mono text-[0.7rem] tracking-[0.18em] text-muted-foreground uppercase">
                {group.label}
              </Autocomplete.GroupLabel>
              <Autocomplete.Collection>
                {(item: CommandItem) => (
                  <Row
                    key={`${group.value}:${item.id}`}
                    item={item}
                    pinned={memory.pinned.includes(item.id)}
                    onRun={() => void run(item)}
                    disabled={item.run.kind === "submit" && (!query.trim() || !!item.hint)}
                  />
                )}
              </Autocomplete.Collection>
            </Autocomplete.Group>
          )}
        </Autocomplete.List>
      </div>

      <div
        id="command-menu-keys"
        className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t bg-muted/50 px-3 py-2 text-xs text-muted-foreground"
      >
        <span className="flex items-center gap-1.5">
          <Keys k="↑" />
          <Keys k="↓" /> to move
        </span>
        <span className="flex items-center gap-1.5">
          <Keys k="Enter" /> {isInput(step) ? "to run" : "to open"}
        </span>
        {(step.kind === "root" || step.kind === "node") && (
          <span className="hidden items-center gap-1.5 sm:flex">
            <Keys k="Tab" /> its commands
          </span>
        )}
        {stack.length > 1 && (
          <span className="hidden items-center gap-1.5 sm:flex">
            <Keys k="Backspace" /> back
          </span>
        )}
        <button
          type="button"
          onClick={openHelp}
          className="ml-auto flex items-center gap-1.5 hover:text-foreground"
          aria-label="Keyboard shortcuts"
        >
          <Keys k="?" /> <span className="hidden sm:inline">Shortcuts</span>
        </button>
      </div>
    </Autocomplete.Root>
  );
}

function list2(label: string, items: CommandItem[]): Group {
  return { value: "schedules", label, items };
}

// Row is one result: its icon, label, and second line, with the keys it
// takes on the right.
function Row({
  item,
  pinned,
  disabled,
  onRun,
}: {
  item: CommandItem;
  pinned: boolean;
  disabled: boolean;
  onRun: () => void;
}) {
  const Icon = icons[item.icon] ?? PanelTop;
  return (
    <Autocomplete.Item
      value={item}
      disabled={disabled}
      onClick={onRun}
      className="group mx-1 flex min-h-10 cursor-default items-center gap-3 rounded-md px-2 py-1.5 text-sm outline-none select-none [scroll-margin-block:0.25rem] data-disabled:opacity-60 data-highlighted:bg-accent data-highlighted:text-accent-foreground"
    >
      <span
        className={cn(
          "flex size-7 shrink-0 items-center justify-center rounded-md border bg-background text-muted-foreground",
          item.destructive && "text-destructive",
        )}
      >
        <Icon className="size-3.5" aria-hidden />
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className={cn("truncate", item.destructive && "text-destructive")}>{item.label}</span>
        {item.hint && (
          <span
            className={cn(
              "truncate text-xs text-muted-foreground",
              item.run.kind === "submit" && "text-destructive",
            )}
          >
            {item.hint}
          </span>
        )}
      </span>
      {pinned && <Pin className="size-3.5 shrink-0 text-muted-foreground" aria-label="Pinned" />}
      {item.shortcut && <Keys k={item.shortcut} className="shrink-0" />}
      {item.drill && (
        <span className="hidden shrink-0 items-center gap-1 text-xs text-muted-foreground group-data-highlighted:flex">
          <Keys k="Tab" />
          <span className="sr-only">for its commands</span>
        </span>
      )}
      {item.run.kind === "open" && (
        <ExternalLink
          className="size-3.5 shrink-0 text-muted-foreground"
          aria-label="Opens in a new tab"
        />
      )}
    </Autocomplete.Item>
  );
}
