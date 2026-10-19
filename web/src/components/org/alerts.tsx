import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { BellRing, Loader2, Pencil, Plus, Send, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { select } from "@/components/backup-destinations";
import { useConfirm } from "@/components/confirm";
import { StatusBadge } from "@/components/server-status";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  type AlertChannel,
  type AlertCondition,
  type AlertRule,
  AlertService,
} from "@/gen/raptor/panel/v1/alert_pb";
import { type Member, OrgService, Role } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { dateTime } from "@/lib/format";
import { useOrgNodes, useOrgServers } from "@/lib/org-data";

const typeNames: Record<string, string> = {
  email: "Email",
  discord: "Discord",
  slack: "Slack",
  telegram: "Telegram",
  ntfy: "ntfy",
  webhook: "Webhook",
};

// Alerts is an org's alerts (docs/PANEL.md#alerts): what's worth one and
// who hears about it, the team's channels, and what was sent. How each
// person is told is theirs to set (MyChannels, from the bell).
export function Alerts({ orgId }: { orgId: string }) {
  const channels = useQuery(AlertService.method.listAlertChannels, { orgId });
  const rules = useQuery(AlertService.method.listAlertRules, { orgId });
  const error = channels.error ?? rules.error;
  if (error) return <p className="text-sm text-destructive">{message(error)}</p>;
  if (!channels.data || !rules.data)
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  return (
    <div className="flex flex-col gap-6">
      <Rules
        orgId={orgId}
        rules={rules.data.rules}
        conditions={rules.data.conditions}
        channels={channels.data.channels}
      />
      <Channels orgId={orgId} channels={channels.data.channels} types={channels.data.types} />
      <History orgId={orgId} channels={channels.data.channels} />
    </div>
  );
}

// MyChannels is how the signed-in user is told about alerts sent to them,
// beside their inbox: their own channels.
export function MyChannels() {
  const channels = useQuery(AlertService.method.listAlertChannels, { orgId: "" });
  if (channels.error) return <p className="text-sm text-destructive">{message(channels.error)}</p>;
  if (!channels.data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  return <ChannelList orgId="" channels={channels.data.channels} types={channels.data.types} />;
}

function useRefresh() {
  const client = useQueryClient();
  return () => client.invalidateQueries();
}

// Channels are an org's team channels: a Discord or Slack channel, a
// group chat, a webhook, which everyone on the team reads.
function Channels({
  orgId,
  channels,
  types,
}: {
  orgId: string;
  channels: AlertChannel[];
  types: string[];
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Team channels</CardTitle>
        <CardDescription>
          Places the whole team reads, like a Discord or Slack channel. Rules can post to them as
          well as telling people.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ChannelList orgId={orgId} channels={channels} types={types} />
      </CardContent>
    </Card>
  );
}

// ChannelList lists channels, an org's (orgId) or the user's own (no
// orgId), with adding, testing, editing, and removing.
function ChannelList({
  orgId,
  channels,
  types,
}: {
  orgId: string;
  channels: AlertChannel[];
  types: string[];
}) {
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<AlertChannel>();
  return (
    <div className="flex flex-col gap-3">
      {channels.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {orgId ? "No team channels." : "None: you're only told here, in the app."}
        </p>
      ) : (
        <ul className="divide-y rounded-lg border">
          {channels.map((c) => (
            <ChannelRow key={c.id} orgId={orgId} channel={c} onEdit={() => setEditing(c)} />
          ))}
        </ul>
      )}
      {types.length > 0 && (
        <div>
          <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
            <Plus /> {orgId ? "Add a team channel" : "Add a way to be told"}
          </Button>
        </div>
      )}
      {adding && <ChannelDialog orgId={orgId} types={types} onClose={() => setAdding(false)} />}
      {editing && (
        <ChannelDialog
          orgId={orgId}
          types={types}
          channel={editing}
          onClose={() => setEditing(undefined)}
        />
      )}
    </div>
  );
}

function ChannelRow({
  orgId,
  channel: c,
  onEdit,
}: {
  orgId: string;
  channel: AlertChannel;
  onEdit: () => void;
}) {
  const refresh = useRefresh();
  const ask = useConfirm();
  const test = useMutation(AlertService.method.testAlertChannel);
  const update = useMutation(AlertService.method.updateAlertChannel);
  const remove = useMutation(AlertService.method.deleteAlertChannel);
  const [note, setNote] = useState<{ ok: boolean; text: string }>();
  const status = !c.enabled
    ? { label: "Off", tone: "stopped" as const }
    : c.failingSince
      ? { label: "Failing", tone: "failed" as const }
      : { label: "On", tone: "live" as const };
  return (
    <li className="flex flex-col gap-1 px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-sm font-medium">{c.name}</span>
          <span className="truncate text-xs text-muted-foreground">
            {typeNames[c.type] ?? c.type} · {c.target || "—"}
          </span>
        </span>
        <StatusBadge status={status} />
        <Switch
          checked={c.enabled}
          aria-label={c.enabled ? `Turn ${c.name} off` : `Turn ${c.name} on`}
          disabled={update.isPending}
          onCheckedChange={async (on) => {
            setNote(undefined);
            try {
              await update.mutateAsync({ orgId, id: c.id, name: c.name, enabled: on });
              await refresh();
            } catch (err) {
              setNote({ ok: false, text: message(err) });
            }
          }}
        />
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={`Send a test to ${c.name}`}
          disabled={test.isPending}
          onClick={async () => {
            setNote(undefined);
            try {
              const res = await test.mutateAsync({ orgId, id: c.id });
              setNote(
                res.ok
                  ? { ok: true, text: "Test sent. Check that it arrived." }
                  : { ok: false, text: res.error },
              );
              await refresh();
            } catch (err) {
              setNote({ ok: false, text: message(err) });
            }
          }}
        >
          {test.isPending ? <Loader2 className="animate-spin" /> : <Send />}
        </Button>
        <Button size="icon-sm" variant="ghost" aria-label={`Edit ${c.name}`} onClick={onEdit}>
          <Pencil />
        </Button>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={`Remove ${c.name}`}
          className="text-muted-foreground hover:text-destructive"
          disabled={remove.isPending}
          onClick={async () => {
            const ok = await ask.confirm({
              title: `Remove ${c.name}?`,
              description: orgId
                ? "Rules stop posting to it."
                : "You won't be told there any more.",
              confirm: "Remove",
              destructive: true,
            });
            if (!ok) return;
            try {
              await remove.mutateAsync({ orgId, id: c.id });
              await refresh();
            } catch (err) {
              setNote({ ok: false, text: message(err) });
            }
          }}
        >
          <Trash2 />
        </Button>
      </div>
      {c.error && (
        <p className="text-xs text-destructive">
          {c.enabled ? "Failing: " : "Turned off after failing for 3 days: "}
          {c.error}
        </p>
      )}
      {note && (
        <p className={note.ok ? "text-xs text-muted-foreground" : "text-xs text-destructive"}>
          {note.text}
        </p>
      )}
    </li>
  );
}

function Field({
  id,
  label,
  hint,
  children,
}: {
  id: string;
  label: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

function ChannelDialog({
  orgId,
  types,
  channel,
  onClose,
}: {
  orgId: string;
  types: string[];
  channel?: AlertChannel;
  onClose: () => void;
}) {
  const refresh = useRefresh();
  const create = useMutation(AlertService.method.createAlertChannel);
  const update = useMutation(AlertService.method.updateAlertChannel);
  const [type, setType] = useState(channel?.type ?? types[0] ?? "ntfy");
  const [name, setName] = useState(channel?.name ?? "");
  const [url, setUrl] = useState("");
  const [secret, setSecret] = useState("");
  const [ntfyServer, setNtfyServer] = useState("");
  const [ntfyTopic, setNtfyTopic] = useState("");
  const [ntfyToken, setNtfyToken] = useState("");
  const [error, setError] = useState("");
  const editing = !!channel;
  const busy = create.isPending || update.isPending;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    const fields = { url, secret, ntfyServer, ntfyTopic, ntfyToken };
    try {
      if (channel) {
        await update.mutateAsync({
          orgId,
          id: channel.id,
          name,
          enabled: channel.enabled,
          ...fields,
        });
      } else {
        await create.mutateAsync({ orgId, type, name, ...fields });
      }
      await refresh();
      onClose();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>
              {editing
                ? `Edit ${channel.name}`
                : orgId
                  ? "Add a team channel"
                  : "Add a way to be told"}
            </DialogTitle>
            <DialogDescription>
              {editing
                ? "Leave the address empty to keep the one it has."
                : "Send a test once it's added, to see it arrive."}
            </DialogDescription>
          </DialogHeader>
          {!editing && (
            <Field id="channel-type" label="Type">
              <select
                id="channel-type"
                className={select}
                value={type}
                onChange={(e) => setType(e.target.value)}
              >
                {types.map((t) => (
                  <option key={t} value={t}>
                    {typeNames[t] ?? t}
                  </option>
                ))}
              </select>
            </Field>
          )}
          {type === "telegram" ? (
            <TelegramConnect orgId={orgId} />
          ) : (
            <>
              <Field id="channel-name" label="Name">
                <Input
                  id="channel-name"
                  value={name}
                  maxLength={60}
                  placeholder={typeNames[type]}
                  onChange={(e) => setName(e.target.value)}
                />
              </Field>
              {type === "email" && (
                <p className="text-sm text-muted-foreground">
                  Alerts are emailed to your account's address. Change it in your account settings.
                </p>
              )}
              {(type === "discord" || type === "slack" || type === "webhook") && (
                <Field
                  id="channel-url"
                  label={type === "webhook" ? "URL" : "Webhook URL"}
                  hint={
                    type === "discord"
                      ? "In Discord: the channel's settings, Integrations, Webhooks, New Webhook, Copy Webhook URL."
                      : type === "slack"
                        ? "In Slack: an app with Incoming Webhooks, then Add New Webhook to Workspace."
                        : "Raptor POSTs JSON here, signed with the secret below (docs: Alerts, webhooks)."
                  }
                >
                  <Input
                    id="channel-url"
                    type="url"
                    value={url}
                    required={!editing}
                    placeholder={editing ? "Unchanged" : "https://…"}
                    onChange={(e) => setUrl(e.target.value)}
                  />
                </Field>
              )}
              {type === "webhook" && (
                <Field
                  id="channel-secret"
                  label="Signing secret (optional)"
                  hint="Signs each request in X-Raptor-Signature, so your endpoint can tell it's from Raptor."
                >
                  <Input
                    id="channel-secret"
                    value={secret}
                    autoComplete="off"
                    onChange={(e) => setSecret(e.target.value)}
                  />
                </Field>
              )}
              {type === "ntfy" && (
                <>
                  <Field
                    id="channel-topic"
                    label="Topic"
                    hint="Anyone who knows the topic can read it on ntfy.sh: make it long and hard to guess, and subscribe to it in the ntfy app."
                  >
                    <Input
                      id="channel-topic"
                      value={ntfyTopic}
                      required={!editing}
                      placeholder={editing ? "Unchanged" : "raptor-7f3k2m9q"}
                      onChange={(e) => setNtfyTopic(e.target.value)}
                    />
                  </Field>
                  <Field id="channel-ntfy-server" label="Server (optional)" hint="Empty: ntfy.sh.">
                    <Input
                      id="channel-ntfy-server"
                      type="url"
                      value={ntfyServer}
                      placeholder="https://ntfy.sh"
                      onChange={(e) => setNtfyServer(e.target.value)}
                    />
                  </Field>
                  <Field id="channel-ntfy-token" label="Access token (optional)">
                    <Input
                      id="channel-ntfy-token"
                      value={ntfyToken}
                      autoComplete="off"
                      onChange={(e) => setNtfyToken(e.target.value)}
                    />
                  </Field>
                </>
              )}
            </>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {type === "telegram" ? "Done" : "Cancel"}
            </Button>
            {type !== "telegram" && (
              <Button type="submit" disabled={busy}>
                {busy && <Loader2 className="animate-spin" />}
                {editing ? "Save" : "Add"}
              </Button>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// TelegramConnect makes a link to Raptor's bot; the chat it's opened in
// becomes a channel, and the list picks it up.
function TelegramConnect({ orgId }: { orgId: string }) {
  const connect = useMutation(AlertService.method.connectTelegram);
  const channels = useQuery(
    AlertService.method.listAlertChannels,
    { orgId },
    { refetchInterval: connect.data ? 3000 : false },
  );
  const [error, setError] = useState("");
  const d = connect.data;
  const connected = channels.data?.channels.filter((c) => c.type === "telegram") ?? [];
  return (
    <div className="flex flex-col gap-3 text-sm">
      {!d ? (
        <>
          <p className="text-muted-foreground">
            {orgId
              ? "Raptor's bot posts alerts to a group chat it's added to, or a private chat."
              : "Raptor's bot sends you your alerts in a private chat, or in a group it's added to."}
          </p>
          <div>
            <Button
              type="button"
              disabled={connect.isPending}
              onClick={async () => {
                setError("");
                try {
                  await connect.mutateAsync({ orgId });
                } catch (err) {
                  setError(message(err));
                }
              }}
            >
              {connect.isPending ? <Loader2 className="animate-spin" /> : <BellRing />}
              Connect Telegram
            </Button>
          </div>
        </>
      ) : (
        <>
          <p className="text-muted-foreground">
            Open one of these in Telegram and press Start. The links work once, for 15 minutes.
          </p>
          <div className="flex flex-wrap gap-2">
            <a href={d.chatUrl} target="_blank" rel="noreferrer">
              <Button type="button" variant="outline">
                A chat with me
              </Button>
            </a>
            <a href={d.groupUrl} target="_blank" rel="noreferrer">
              <Button type="button" variant="outline">
                Add to a group
              </Button>
            </a>
          </div>
          <p className="text-xs text-muted-foreground">
            {connected.length
              ? `Connected: ${connected.map((c) => c.target).join(", ")}.`
              : "Waiting for Telegram…"}
          </p>
        </>
      )}
      {error && <p className="text-destructive">{error}</p>}
    </div>
  );
}

function scopeText(r: AlertRule, nodes: Map<string, string>, servers: Map<string, string>): string {
  if (r.serverId)
    return `${servers.get(`${r.nodeId}/${r.serverId}`) ?? r.serverId} on ${nodes.get(r.nodeId) ?? "a node"}`;
  if (r.nodeId) return nodes.get(r.nodeId) ?? "A node";
  return "Every node";
}

const roleOptions = [
  { id: "owner", label: "Owners" },
  { id: "admin", label: "Admins" },
  { id: "member", label: "Members" },
];

const roleOf: Record<Role, string> = {
  [Role.UNSPECIFIED]: "",
  [Role.MEMBER]: "member",
  [Role.ADMIN]: "admin",
  [Role.OWNER]: "owner",
};

// audienceText is who a rule reaches: "Owners, Bob · #ops".
function audienceText(
  r: AlertRule,
  members: Map<string, string>,
  channels: Map<string, string>,
): string {
  const people = [
    ...roleOptions.filter((o) => r.notifyRoles.includes(o.id)).map((o) => o.label),
    ...r.notifyUserIds.map((id) => members.get(id) ?? "a former member"),
  ];
  const chans = r.channelIds.map((id) => channels.get(id) ?? "?");
  return [people.join(", "), chans.join(", ")].filter(Boolean).join(" · ") || "nobody";
}

function memberName(m: Member): string {
  return m.name || m.email;
}

function conditionText(r: AlertRule, c?: AlertCondition): string {
  if (!c) return r.condition;
  if (c.threshold) return `${c.label} ${r.threshold}${c.unit} for ${r.forMinutes} min`;
  if (c.id === "node_offline") return `${c.label} for ${r.forMinutes} min`;
  return c.label;
}

function Rules({
  orgId,
  rules,
  conditions,
  channels,
}: {
  orgId: string;
  rules: AlertRule[];
  conditions: AlertCondition[];
  channels: AlertChannel[];
}) {
  const refresh = useRefresh();
  const save = useMutation(AlertService.method.saveAlertRule);
  const remove = useMutation(AlertService.method.deleteAlertRule);
  const nodes = useOrgNodes(orgId).data?.nodes ?? [];
  const { servers } = useOrgServers(orgId, 30_000);
  const nodeNames = new Map(nodes.map((n) => [n.id, n.name]));
  const serverNames = new Map(servers.map((s) => [`${s.node.id}/${s.id}`, s.name]));
  const channelNames = new Map(channels.map((c) => [c.id, c.name]));
  const members = useQuery(OrgService.method.listMembers, { orgId });
  const memberNames = new Map((members.data?.members ?? []).map((m) => [m.userId, memberName(m)]));
  const byId = new Map(conditions.map((c) => [c.id, c]));
  const [editing, setEditing] = useState<AlertRule | "new">();
  const [error, setError] = useState("");
  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="space-y-1.5">
          <CardTitle>Rules</CardTitle>
          <CardDescription>
            What's worth an alert, and who hears about it. Each person picks how they're told, from
            the bell at the top of the page. Thresholds are checked on the node every 10 seconds.
          </CardDescription>
        </div>
        <Button size="sm" onClick={() => setEditing("new")}>
          <Plus /> Add rule
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {rules.length === 0 ? (
          <p className="text-sm text-muted-foreground">No rules: nothing alerts.</p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {rules.map((r) => (
              <li key={r.id} className="flex flex-wrap items-center gap-2 px-3 py-2">
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-sm font-medium">
                    {r.name || conditionText(r, byId.get(r.condition))}
                  </span>
                  <span className="truncate text-xs text-muted-foreground">
                    {r.name ? `${conditionText(r, byId.get(r.condition))} · ` : ""}
                    {scopeText(r, nodeNames, serverNames)} →{" "}
                    {audienceText(r, memberNames, channelNames)}
                  </span>
                </span>
                <Switch
                  checked={r.enabled}
                  aria-label={r.enabled ? "Turn the rule off" : "Turn the rule on"}
                  disabled={save.isPending}
                  onCheckedChange={async (on) => {
                    setError("");
                    try {
                      await save.mutateAsync({ orgId, rule: { ...r, enabled: on } });
                      await refresh();
                    } catch (err) {
                      setError(message(err));
                    }
                  }}
                />
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Edit the rule"
                  onClick={() => setEditing(r)}
                >
                  <Pencil />
                </Button>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Remove the rule"
                  className="text-muted-foreground hover:text-destructive"
                  disabled={remove.isPending}
                  onClick={async () => {
                    setError("");
                    try {
                      await remove.mutateAsync({ orgId, id: r.id });
                      await refresh();
                    } catch (err) {
                      setError(message(err));
                    }
                  }}
                >
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      {editing && (
        <RuleDialog
          orgId={orgId}
          rule={editing === "new" ? undefined : editing}
          conditions={conditions}
          channels={channels}
          onClose={() => setEditing(undefined)}
        />
      )}
    </Card>
  );
}

function RuleDialog({
  orgId,
  rule,
  conditions,
  channels,
  onClose,
}: {
  orgId: string;
  rule?: AlertRule;
  conditions: AlertCondition[];
  channels: AlertChannel[];
  onClose: () => void;
}) {
  const refresh = useRefresh();
  const save = useMutation(AlertService.method.saveAlertRule);
  const nodes = useOrgNodes(orgId).data?.nodes ?? [];
  const { servers } = useOrgServers(orgId, 30_000);
  const [condition, setCondition] = useState(rule?.condition ?? "crash");
  const [name, setName] = useState(rule?.name ?? "");
  const [nodeId, setNodeId] = useState(rule?.nodeId ?? "");
  const [serverId, setServerId] = useState(rule?.serverId ?? "");
  const [threshold, setThreshold] = useState(String(rule?.threshold || 90));
  const [forMinutes, setForMinutes] = useState(String(rule?.forMinutes || 5));
  const members = useQuery(OrgService.method.listMembers, { orgId });
  const [picked, setPicked] = useState<string[]>(rule?.channelIds ?? []);
  const [roles, setRoles] = useState<string[]>(rule?.notifyRoles ?? ["owner", "admin"]);
  const [people, setPeople] = useState<string[]>(rule?.notifyUserIds ?? []);
  // People already told by a picked role aren't listed to pick again.
  const others = (members.data?.members ?? []).filter((m) => !roles.includes(roleOf[m.role]));
  const [resolved, setResolved] = useState(rule?.notifyResolved ?? true);
  const [error, setError] = useState("");
  const c = conditions.find((x) => x.id === condition);
  const timed = c?.threshold || condition === "node_offline";
  const nodeServers = servers.filter((s) => s.node.id === nodeId);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await save.mutateAsync({
        orgId,
        rule: {
          id: rule?.id ?? "",
          name,
          condition,
          nodeId,
          serverId: c?.scope === "server" ? serverId : "",
          threshold: c?.threshold ? Number(threshold) : 0,
          forMinutes: timed ? Number(forMinutes) : 0,
          channelIds: picked,
          notifyRoles: roles,
          notifyUserIds: people.filter((id) => others.some((m) => m.userId === id)),
          notifyResolved: resolved,
          enabled: rule?.enabled ?? true,
        },
      });
      await refresh();
      onClose();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{rule ? "Edit the rule" : "Add a rule"}</DialogTitle>
            <DialogDescription>
              Everyone picked is told in Raptor and the ways they've set up for themselves.
            </DialogDescription>
          </DialogHeader>
          <Field id="rule-condition" label="When">
            <select
              id="rule-condition"
              className={select}
              value={condition}
              onChange={(e) => setCondition(e.target.value)}
            >
              <optgroup label="Servers">
                {conditions
                  .filter((x) => x.scope === "server")
                  .map((x) => (
                    <option key={x.id} value={x.id}>
                      {x.label}
                      {x.threshold ? "…" : ""}
                    </option>
                  ))}
              </optgroup>
              <optgroup label="Nodes">
                {conditions
                  .filter((x) => x.scope === "node")
                  .map((x) => (
                    <option key={x.id} value={x.id}>
                      {x.label}
                      {x.threshold ? "…" : ""}
                    </option>
                  ))}
              </optgroup>
            </select>
          </Field>
          {timed && (
            <div className="grid grid-cols-2 gap-3">
              {c?.threshold && (
                <Field id="rule-threshold" label={`Above (${c.unit})`}>
                  <Input
                    id="rule-threshold"
                    type="number"
                    min={1}
                    max={100}
                    value={threshold}
                    onChange={(e) => setThreshold(e.target.value)}
                  />
                </Field>
              )}
              <Field id="rule-for" label="For (minutes)">
                <Input
                  id="rule-for"
                  type="number"
                  min={1}
                  max={1440}
                  value={forMinutes}
                  onChange={(e) => setForMinutes(e.target.value)}
                />
              </Field>
            </div>
          )}
          {c?.threshold && (
            <p className="-mt-2 text-xs text-muted-foreground">
              {condition === "cpu_high"
                ? "Of the server's CPU limit, or of the whole machine if it has none."
                : condition === "memory_high"
                  ? "Of the server's memory limit."
                  : condition === "disk_high"
                    ? "Of the server's disk limit. Servers without one aren't checked."
                    : "Of the whole machine."}{" "}
              Needs Wings v0.5.0 or later on the node.
            </p>
          )}
          <Field id="rule-node" label="Node">
            <select
              id="rule-node"
              className={select}
              value={nodeId}
              onChange={(e) => {
                setNodeId(e.target.value);
                setServerId("");
              }}
            >
              <option value="">Every node</option>
              {nodes.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.name}
                </option>
              ))}
            </select>
          </Field>
          {c?.scope === "server" && nodeId && (
            <Field id="rule-server" label="Server">
              <select
                id="rule-server"
                className={select}
                value={serverId}
                onChange={(e) => setServerId(e.target.value)}
              >
                <option value="">Every server on it</option>
                {nodeServers.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            </Field>
          )}
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-1.5 font-mono text-xs leading-none tracking-wide uppercase">
              Tell
            </legend>
            <div className="flex flex-wrap gap-x-4 gap-y-2">
              {roleOptions.map((o) => (
                <label key={o.id} className="flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={roles.includes(o.id)}
                    onCheckedChange={(on) =>
                      setRoles(on ? [...roles, o.id] : roles.filter((x) => x !== o.id))
                    }
                  />
                  {o.label}
                </label>
              ))}
            </div>
            {others.length > 0 && (
              <div className="flex max-h-36 flex-col gap-2 overflow-auto rounded-lg border p-2">
                {others.map((m) => (
                  <label key={m.userId} className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={people.includes(m.userId)}
                      onCheckedChange={(on) =>
                        setPeople(on ? [...people, m.userId] : people.filter((x) => x !== m.userId))
                      }
                    />
                    <span className="truncate">{memberName(m)}</span>
                    {m.name && (
                      <span className="truncate text-xs text-muted-foreground">{m.email}</span>
                    )}
                  </label>
                ))}
              </div>
            )}
            <p className="text-xs text-muted-foreground">
              Members are only told about servers they have access to.
            </p>
          </fieldset>
          {channels.length > 0 && (
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-1.5 font-mono text-xs leading-none tracking-wide uppercase">
                Also post to
              </legend>
              {channels.map((ch) => (
                <label key={ch.id} className="flex items-center gap-2 text-sm">
                  <Checkbox
                    checked={picked.includes(ch.id)}
                    onCheckedChange={(on) =>
                      setPicked(on ? [...picked, ch.id] : picked.filter((x) => x !== ch.id))
                    }
                  />
                  {ch.name}
                  <span className="text-xs text-muted-foreground">{typeNames[ch.type]}</span>
                </label>
              ))}
            </fieldset>
          )}
          {c?.resolves && (
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={resolved} onCheckedChange={(on) => setResolved(on)} />
              Also say when it's over
            </label>
          )}
          <Field id="rule-name" label="Name (optional)">
            <Input
              id="rule-name"
              value={name}
              maxLength={60}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={
                save.isPending ||
                (picked.length === 0 &&
                  roles.length === 0 &&
                  !people.some((id) => others.some((m) => m.userId === id)))
              }
            >
              {save.isPending && <Loader2 className="animate-spin" />}
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const statusTone = {
  sent: { label: "Sent", tone: "live" },
  pending: { label: "Sending", tone: "pending" },
  failed: { label: "Failed", tone: "failed" },
  dropped: { label: "Not sent", tone: "stopped" },
} as const;

function History({ orgId, channels }: { orgId: string; channels: AlertChannel[] }) {
  const history = useQuery(
    AlertService.method.listAlertHistory,
    { orgId, limit: 50 },
    { refetchInterval: 15_000 },
  );
  const names = new Map(channels.map((c) => [c.id, c.name]));
  const entries = history.data?.entries ?? [];
  return (
    <Card>
      <CardHeader>
        <CardTitle>Recent alerts</CardTitle>
        <CardDescription>
          The last 50, one line for each channel and each person's own way of being told. Kept 30
          days.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {history.error && <p className="text-sm text-destructive">{message(history.error)}</p>}
        {entries.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing yet.</p>
        ) : (
          <ul className="divide-y rounded-lg border">
            {entries.map((e) => (
              <li key={e.id} className="flex flex-wrap items-center gap-2 px-3 py-2">
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-sm">{e.title}</span>
                  <span className="truncate text-xs text-muted-foreground">
                    {[e.serverName, e.nodeName].filter(Boolean).join(" on ") || "—"} ·{" "}
                    {e.channelLabel || names.get(e.channelId) || "a removed channel"} ·{" "}
                    {e.at ? dateTime(timestampDate(e.at)) : ""}
                  </span>
                  {e.error && e.status !== "sent" && (
                    <span className="text-xs text-destructive">{e.error}</span>
                  )}
                </span>
                <StatusBadge
                  status={statusTone[e.status as keyof typeof statusTone] ?? statusTone.pending}
                />
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
