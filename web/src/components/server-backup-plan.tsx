import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Settings2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { nodeCommand, select } from "@/components/backup-destinations";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import {
  type DestinationType,
  describeRetention,
  needsPasskey,
  type Policy,
  presetOf,
  type Retention,
  retentionPresets,
  type Target,
  typeNames,
} from "@/lib/backup-destinations";
import { message } from "@/lib/errors";
import { sendSigned } from "@/lib/signed";
import { commandClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

// What backup.policy returns.
export type PlanData = {
  policy: Policy;
  destinations: { id: string; name: string; type: DestinationType }[];
};

export function useBackupPlan(nodeId: string, serverId: string) {
  return useQuery({
    queryKey: ["backup-policy", nodeId, serverId],
    queryFn: () => nodeCommand<PlanData>(nodeId, "backup.policy", {}, serverId),
  });
}

// BackupPlan is where a server's backups go and how many each place keeps.
// Admins change it; changes that keep fewer backups or send them somewhere
// new are signed by the passkey.
export function BackupPlan({
  orgId,
  nodeId,
  serverId,
  userId,
  admin,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  admin: boolean;
}) {
  const plan = useBackupPlan(nodeId, serverId);
  const [editing, setEditing] = useState(false);
  const data = plan.data;
  const name = (id: string) => data?.destinations.find((d) => d.id === id);

  return (
    <Card>
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="space-y-1.5">
          <CardTitle>Where backups go</CardTitle>
          <CardDescription>
            Each backup is taken to every place below, and each keeps its own number of them.
          </CardDescription>
        </div>
        {admin && data && (
          <Button size="sm" variant="outline" onClick={() => setEditing(true)}>
            <Settings2 /> Change
          </Button>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        {plan.error && <p className="text-sm text-destructive">{message(plan.error)}</p>}
        {!data && !plan.error && <p className="text-sm text-muted-foreground">Asking the node…</p>}
        {data && (
          <ul className="divide-y rounded-lg border">
            {data.policy.targets.map((t, i) => {
              const d = name(t.destination_id);
              return (
                <li
                  key={t.destination_id}
                  className="flex flex-wrap items-center gap-x-3 gap-y-1 p-3"
                >
                  <span className="text-sm font-medium">{d?.name ?? "A removed destination"}</span>
                  {d && <Badge variant="outline">{typeNames[d.type]}</Badge>}
                  {i === 0 && data.policy.targets.length > 1 && (
                    <Badge variant="secondary">Primary</Badge>
                  )}
                  <span className="text-sm text-muted-foreground sm:ml-auto">
                    {describeRetention(t)}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
        {data?.policy.ignore?.length ? (
          <p className="text-xs text-muted-foreground">
            Leaves out {data.policy.ignore.length}{" "}
            {data.policy.ignore.length === 1 ? "pattern" : "patterns"}:{" "}
            <code>{data.policy.ignore.slice(0, 3).join(", ")}</code>
            {data.policy.ignore.length > 3 && "…"}
          </p>
        ) : null}
      </CardContent>
      {editing && data && (
        <PlanDialog
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          userId={userId}
          data={data}
          onClose={() => setEditing(false)}
        />
      )}
    </Card>
  );
}

type Row = { on: boolean; preset: string; keep: Retention };

function PlanDialog({
  orgId,
  nodeId,
  serverId,
  userId,
  data,
  onClose,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  userId: string;
  data: PlanData;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const standard = retentionPresets[1]?.keep as Retention;
  const [rows, setRows] = useState<Record<string, Row>>(() =>
    Object.fromEntries(
      data.destinations.map((d) => {
        const t = data.policy.targets.find((x) => x.destination_id === d.id);
        const keep = t ?? standard;
        return [d.id, { on: !!t, preset: presetOf(keep), keep: { ...keep } }];
      }),
    ),
  );
  const [ignore, setIgnore] = useState((data.policy.ignore ?? []).join("\n"));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  // The order: the current targets first, as they were, then new ones in
  // the node's order. The first is the primary.
  const order = [
    ...data.policy.targets.map((t) => t.destination_id).filter((id) => rows[id]),
    ...data.destinations
      .map((d) => d.id)
      .filter((id) => !data.policy.targets.some((t) => t.destination_id === id)),
  ];
  const next: Policy = {
    targets: order
      .filter((id) => rows[id]?.on)
      .map((id): Target => ({ destination_id: id, ...(rows[id] as Row).keep })),
    ignore: ignore
      .split("\n")
      .map((l) => l.trim())
      .filter(Boolean),
  };
  const signed = needsPasskey(next, data.policy);
  const setRow = (id: string, change: Partial<Row>) =>
    setRows({ ...rows, [id]: { ...(rows[id] as Row), ...change } });

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (signed) {
        await sendSigned({
          userId,
          nodeId,
          serverId,
          action: "backup.policy.update",
          params: next as unknown as Record<string, unknown>,
        });
      } else {
        await commandClient.execute({
          nodeId,
          serverId,
          action: "backup.policy.update",
          paramsJson: JSON.stringify(next),
        });
      }
      await client.invalidateQueries({ queryKey: ["backup-policy", nodeId, serverId] });
      onClose();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <form onSubmit={save} className="flex min-w-0 flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Where backups go</DialogTitle>
            <DialogDescription>
              Pick one or more places, and how many backups each keeps. Locked backups are always
              kept.{" "}
              <Link
                to="/orgs/$orgId/nodes/$nodeId/backups"
                params={{ orgId, nodeId }}
                className="underline underline-offset-2"
              >
                Add a destination
              </Link>
            </DialogDescription>
          </DialogHeader>
          <div className="-mx-1 flex max-h-[60vh] min-w-0 flex-col gap-3 overflow-y-auto px-1">
            {order.map((id) => {
              const d = data.destinations.find((x) => x.id === id);
              const r = rows[id];
              if (!d || !r) return null;
              return (
                <div key={id} className="space-y-2 rounded-lg border p-3">
                  <label className="flex items-center gap-2 text-sm font-medium">
                    <input
                      type="checkbox"
                      checked={r.on}
                      onChange={(e) => setRow(id, { on: e.target.checked })}
                    />
                    {d.name}
                    <span className="font-normal text-muted-foreground">· {typeNames[d.type]}</span>
                  </label>
                  {r.on && (
                    <div className="space-y-2 pl-6">
                      <select
                        aria-label={`How many backups ${d.name} keeps`}
                        className={select}
                        value={r.preset}
                        onChange={(e) => {
                          const p = retentionPresets.find((x) => x.id === e.target.value);
                          setRow(id, { preset: e.target.value, keep: p ? { ...p.keep } : r.keep });
                        }}
                      >
                        {retentionPresets.map((p) => (
                          <option key={p.id} value={p.id}>
                            {p.name}: {p.hint}
                          </option>
                        ))}
                        <option value="custom">Custom</option>
                      </select>
                      {r.preset === "custom" && (
                        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                          {(
                            [
                              ["keep_last", "Last"],
                              ["keep_daily", "Daily"],
                              ["keep_weekly", "Weekly"],
                              ["keep_monthly", "Monthly"],
                            ] as const
                          ).map(([k, label]) => (
                            <div key={k} className="grid gap-1">
                              <Label htmlFor={`${id}-${k}`} className="text-xs">
                                {label}
                              </Label>
                              <Input
                                id={`${id}-${k}`}
                                type="number"
                                min={0}
                                max={1000}
                                value={r.keep[k]}
                                onChange={(e) =>
                                  setRow(id, {
                                    keep: {
                                      ...r.keep,
                                      [k]: Math.max(0, Number(e.target.value) || 0),
                                    },
                                  })
                                }
                              />
                            </div>
                          ))}
                        </div>
                      )}
                      <p className="text-xs text-muted-foreground">{describeRetention(r.keep)}</p>
                    </div>
                  )}
                </div>
              );
            })}
            <div className="grid gap-1.5">
              <Label htmlFor="ignore">Leave out</Label>
              <textarea
                id="ignore"
                rows={3}
                placeholder={"logs/\n*.tmp"}
                className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 font-mono text-xs outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
                value={ignore}
                onChange={(e) => setIgnore(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                One pattern a line, like a .gitignore. A .pteroignore file in the server works too.
              </p>
            </div>
          </div>
          {signed && (
            <p className="text-xs text-muted-foreground">
              This sends backups somewhere new or keeps fewer of them, so it needs your passkey.
            </p>
          )}
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || next.targets.length === 0}>
              {busy ? "Saving…" : signed ? "Save with a passkey" : "Save"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
