import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  CalendarClock,
  CircleCheck,
  CircleX,
  LoaderCircle,
  Plus,
  SkipForward,
  Trash2,
} from "lucide-react";
import { type FormEvent, useState } from "react";

import { ActivityCard, type TimelineItem } from "@/components/activity-timeline";
import { EmptyState } from "@/components/page";
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { OrgService, type Schedule, type ScheduleRun } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { when } from "@/lib/format";
import { duration, reasonText, skipText, stepFailure, stepName } from "@/lib/schedule-runs";
import { commandClient, orgClient } from "@/lib/transport";

const select =
  "h-8 rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// A schedule as Wings takes it (schedule.Definition).
type Step = {
  type: "command" | "power" | "wait" | "backup";
  command?: string;
  action?: "start" | "stop" | "restart" | "kill";
  duration?: string;
  continue_on_failure?: boolean;
};

type Definition = {
  name: string;
  cron: string;
  timezone?: string;
  enabled: boolean;
  only_when_online?: boolean;
  missed?: string;
  jitter?: string;
  steps: Step[];
};

const presets: [string, string][] = [
  ["@hourly", "Every hour"],
  ["@daily", "Every day at midnight"],
  ["0 4 * * *", "Every day at 4:00"],
  ["0 */6 * * *", "Every 6 hours"],
  ["@weekly", "Every Sunday at midnight"],
];

function describeStep(s: Step): string {
  switch (s.type) {
    case "command":
      return `Run "${s.command}"`;
    case "power":
      return s.action ? s.action.charAt(0).toUpperCase() + s.action.slice(1) : "Power";
    case "wait":
      return `Wait ${s.duration}`;
    case "backup":
      return "Back up";
  }
}

// timeZones is every IANA zone the browser knows, for the picker.
function timeZones(): string[] {
  const intl = Intl as typeof Intl & { supportedValuesOf?: (k: string) => string[] };
  return ["UTC", ...(intl.supportedValuesOf?.("timeZone") ?? []).filter((z) => z !== "UTC")];
}

function parse(s: Schedule): Definition {
  return JSON.parse(s.definitionJson || "{}") as Definition;
}

// ServerSchedules lists a server's schedules, runs them, turns them on and
// off, and edits them. None are signed: a schedule only does what the user
// could do by hand.
export function ServerSchedules({
  orgId,
  nodeId,
  serverId,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
}) {
  const client = useQueryClient();
  const list = useQuery(
    OrgService.method.listSchedules,
    { orgId, nodeId, serverId },
    { refetchInterval: 10_000 },
  );
  const [editing, setEditing] = useState<{ id?: string; def: Definition } | null>(null);
  const [runsOf, setRunsOf] = useState("");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const schedules = list.data?.schedules ?? [];

  async function run(key: string, action: string, params: Record<string, unknown>) {
    setBusy(key);
    setError("");
    try {
      await commandClient.execute({
        nodeId,
        action,
        serverId,
        paramsJson: JSON.stringify(params),
      });
      await client.invalidateQueries();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader className="flex flex-row items-start justify-between gap-4">
          <div className="space-y-1.5">
            <CardTitle>Schedules</CardTitle>
            <CardDescription>
              Commands, restarts, and backups that run on their own, on the node's clock.
            </CardDescription>
          </div>
          <Button
            size="sm"
            onClick={() =>
              setEditing({
                def: {
                  name: "",
                  cron: "@daily",
                  timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
                  enabled: true,
                  steps: [{ type: "power", action: "restart" }],
                },
              })
            }
          >
            <Plus /> New schedule
          </Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {error && <p className="text-sm text-destructive">{error}</p>}
          {list.error && <p className="text-sm text-destructive">{message(list.error)}</p>}
          {list.data && schedules.length === 0 ? (
            <EmptyState
              icon={CalendarClock}
              title="No schedules"
              description="Restart the server every night, back it up, or send a command on a timer."
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>When</TableHead>
                  <TableHead>Next run</TableHead>
                  <TableHead>Last run</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {schedules.map((s) => {
                  const def = parse(s);
                  return (
                    <TableRow key={s.id}>
                      <TableCell>
                        <button
                          type="button"
                          className="text-left font-medium hover:underline"
                          onClick={() => setEditing({ id: s.id, def })}
                        >
                          {s.name}
                        </button>
                        <p className="text-xs text-muted-foreground">
                          {(def.steps ?? []).map(describeStep).join(" → ")}
                        </p>
                      </TableCell>
                      <TableCell>
                        <code className="text-xs">{def.cron}</code>
                        {def.timezone && def.timezone !== "UTC" && (
                          <span className="text-xs text-muted-foreground"> {def.timezone}</span>
                        )}
                      </TableCell>
                      <TableCell>
                        {s.enabled ? when(s.nextRun) : <Badge variant="outline">Off</Badge>}
                      </TableCell>
                      <TableCell>{when(s.lastRun)}</TableCell>
                      <TableCell>
                        <div className="flex justify-end gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={busy !== ""}
                            onClick={() => run(s.id, "schedule.run", { schedule_id: s.id })}
                          >
                            Run now
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setRunsOf(s.id)}>
                            Runs
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={busy !== ""}
                            onClick={() =>
                              run(s.id, "schedule.update", {
                                schedule_id: s.id,
                                ...def,
                                enabled: !s.enabled,
                              })
                            }
                          >
                            {s.enabled ? "Turn off" : "Turn on"}
                          </Button>
                          <Button
                            size="icon-sm"
                            variant="ghost"
                            aria-label="Delete"
                            className="text-destructive"
                            disabled={busy !== ""}
                            onClick={() => {
                              if (window.confirm(`Delete the schedule "${s.name}"?`))
                                run(s.id, "schedule.delete", { schedule_id: s.id });
                            }}
                          >
                            <Trash2 />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
        {editing && (
          <ScheduleEditor
            key={editing.id ?? "new"}
            initial={editing.def}
            onClose={() => setEditing(null)}
            onSave={async (def) => {
              await commandClient.execute({
                nodeId,
                action: editing.id ? "schedule.update" : "schedule.create",
                serverId,
                paramsJson: JSON.stringify(editing.id ? { schedule_id: editing.id, ...def } : def),
              });
              await client.invalidateQueries();
              setEditing(null);
            }}
          />
        )}
      </Card>
      {schedules.length > 0 && (
        <ScheduleRuns
          orgId={orgId}
          nodeId={nodeId}
          serverId={serverId}
          schedules={schedules}
          scheduleId={runsOf}
          onScheduleId={setRunsOf}
        />
      )}
    </div>
  );
}

const Spinning = ({ className }: { className?: string }) => (
  <LoaderCircle className={`${className ?? ""} animate-spin`} />
);

// ScheduleRuns is the server's schedule runs, newest first: how each went,
// and where a failed one stopped. Runs of a deleted schedule go with it.
function ScheduleRuns({
  orgId,
  nodeId,
  serverId,
  schedules,
  scheduleId,
  onScheduleId,
}: {
  orgId: string;
  nodeId: string;
  serverId: string;
  schedules: Schedule[];
  scheduleId: string;
  onScheduleId: (id: string) => void;
}) {
  const name = (id: string) => schedules.find((s) => s.id === id)?.name ?? "A schedule";

  function toItem(r: ScheduleRun): TimelineItem {
    const started = r.startedAt ? timestampDate(r.startedAt) : undefined;
    const took =
      r.startedAt && r.finishedAt && r.status !== "skipped"
        ? duration(timestampDate(r.finishedAt).getTime() - timestampDate(r.startedAt).getTime())
        : "";
    const steps = `${r.steps.length} ${r.steps.length === 1 ? "step" : "steps"}`;
    const base = { id: r.id, at: started, actor: name(r.scheduleId) };
    switch (r.status) {
      case "running":
        return {
          ...base,
          icon: Spinning,
          text: "is running",
          badge: (
            <Badge variant="secondary" className="ml-2">
              {r.steps.length > 0 ? `${steps} done` : "Started"}
            </Badge>
          ),
          details: [reasonText(r.reason)],
        };
      case "skipped":
        return {
          ...base,
          icon: SkipForward,
          text: `was skipped: ${skipText(r.skipReason)}`,
          details: [
            r.scheduledFor
              ? `Due ${timestampDate(r.scheduledFor).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}`
              : "",
          ],
        };
      case "failed": {
        const at = r.steps.findIndex((st) => !st.ok && st.error);
        const failed = at >= 0 ? r.steps[at] : undefined;
        const others = r.steps.filter((st, i) => !st.ok && i !== at).length;
        return {
          ...base,
          icon: CircleX,
          failed: true,
          text: failed
            ? `failed at step ${at + 1} (${stepName(failed.type)}): ${stepFailure(failed)}`
            : "failed",
          details: [
            reasonText(r.reason),
            took,
            others > 0 ? `${others} more ${others === 1 ? "step" : "steps"} failed` : "",
          ],
        };
      }
    }
    const carriedOn = r.steps.filter((st) => !st.ok).length;
    return {
      ...base,
      icon: CircleCheck,
      text:
        carriedOn > 0
          ? `ran, carrying on past ${carriedOn} failed ${carriedOn === 1 ? "step" : "steps"}`
          : "ran",
      details: [reasonText(r.reason), took, steps],
    };
  }

  return (
    <ActivityCard
      title="Runs"
      description="Each run of the server's schedules, and where a failed one stopped. The last 100 of each are kept."
      queryKey={["schedule-runs", orgId, nodeId, serverId, scheduleId]}
      fetchPage={async (token) => {
        const res = await orgClient.listScheduleRuns({
          orgId,
          nodeId,
          serverId,
          scheduleId,
          pageToken: token,
        });
        return { events: res.runs, next: res.nextPageToken };
      }}
      toItem={toItem}
      empty="No runs yet."
      refetchInterval={5_000}
      action={
        <select
          aria-label="Schedule"
          className={select}
          value={scheduleId}
          onChange={(e) => onScheduleId(e.target.value)}
        >
          <option value="">All schedules</option>
          {schedules.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
      }
    />
  );
}

function ScheduleEditor({
  initial,
  onClose,
  onSave,
}: {
  initial: Definition;
  onClose: () => void;
  onSave: (def: Definition) => Promise<void>;
}) {
  const [def, setDef] = useState<Definition>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const steps = def.steps ?? [];

  function setStep(i: number, s: Step) {
    setDef({ ...def, steps: steps.map((old, j) => (j === i ? s : old)) });
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSave(def);
    } catch (err) {
      setError(message(err));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{initial.name ? `Edit "${initial.name}"` : "New schedule"}</DialogTitle>
            <DialogDescription>
              Steps run in order; a failed step stops the run unless it says to carry on.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="sc-name">Name</Label>
            <Input
              id="sc-name"
              required
              maxLength={100}
              placeholder="Nightly restart"
              value={def.name}
              onChange={(e) => setDef({ ...def, name: e.target.value })}
            />
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-2">
              <Label htmlFor="sc-cron">When (cron)</Label>
              <Input
                id="sc-cron"
                required
                className="font-mono"
                value={def.cron}
                onChange={(e) => setDef({ ...def, cron: e.target.value })}
              />
              <div className="flex flex-wrap gap-1">
                {presets.map(([cron, label]) => (
                  <Button
                    key={cron}
                    type="button"
                    size="xs"
                    variant={def.cron === cron ? "secondary" : "outline"}
                    onClick={() => setDef({ ...def, cron })}
                  >
                    {label}
                  </Button>
                ))}
              </div>
            </div>
            <div className="grid content-start gap-2">
              <Label htmlFor="sc-tz">Time zone</Label>
              <Input
                id="sc-tz"
                placeholder="UTC"
                list="sc-tz-list"
                autoComplete="off"
                value={def.timezone ?? ""}
                onChange={(e) => setDef({ ...def, timezone: e.target.value })}
              />
              <datalist id="sc-tz-list">
                {timeZones().map((z) => (
                  <option key={z} value={z} />
                ))}
              </datalist>
              <p className="text-xs text-muted-foreground">
                Yours is {Intl.DateTimeFormat().resolvedOptions().timeZone}.{" "}
                {def.timezone !== Intl.DateTimeFormat().resolvedOptions().timeZone && (
                  <button
                    type="button"
                    className="underline underline-offset-2 hover:text-foreground"
                    onClick={() =>
                      setDef({ ...def, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone })
                    }
                  >
                    Use it
                  </button>
                )}
              </p>
            </div>
          </div>
          <div className="flex flex-col gap-2 text-sm">
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={def.enabled}
                onChange={(e) => setDef({ ...def, enabled: e.target.checked })}
              />
              On
            </label>
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                checked={def.only_when_online ?? false}
                onChange={(e) => setDef({ ...def, only_when_online: e.target.checked })}
              />
              Skip runs while the server is offline
            </label>
          </div>
          <div className="flex flex-col gap-2">
            <Label>Steps</Label>
            {steps.map((s, i) => (
              <div
                // biome-ignore lint/suspicious/noArrayIndexKey: steps have no IDs; their order is their identity
                key={i}
                className="flex flex-wrap items-center gap-2 rounded-lg border p-2"
              >
                <span className="w-5 text-xs text-muted-foreground tabular-nums">{i + 1}.</span>
                <select
                  aria-label="Step"
                  className={select}
                  value={s.type}
                  onChange={(e) => {
                    const type = e.target.value as Step["type"];
                    setStep(
                      i,
                      type === "power"
                        ? { type, action: "restart" }
                        : type === "wait"
                          ? { type, duration: "30s" }
                          : type === "command"
                            ? { type, command: "" }
                            : { type },
                    );
                  }}
                >
                  <option value="command">Send a command</option>
                  <option value="power">Power</option>
                  <option value="wait">Wait</option>
                  <option value="backup">Back up</option>
                </select>
                {s.type === "command" && (
                  <Input
                    aria-label="Command"
                    required
                    className="min-w-40 flex-1 font-mono"
                    placeholder="say Restarting in 1 minute"
                    value={s.command ?? ""}
                    onChange={(e) => setStep(i, { ...s, command: e.target.value })}
                  />
                )}
                {s.type === "power" && (
                  <select
                    aria-label="Power action"
                    className={select}
                    value={s.action}
                    onChange={(e) => setStep(i, { ...s, action: e.target.value as Step["action"] })}
                  >
                    <option value="start">Start</option>
                    <option value="stop">Stop</option>
                    <option value="restart">Restart</option>
                    <option value="kill">Kill</option>
                  </select>
                )}
                {s.type === "wait" && (
                  <Input
                    aria-label="How long"
                    required
                    className="w-24 font-mono"
                    placeholder="30s"
                    value={s.duration ?? ""}
                    onChange={(e) => setStep(i, { ...s, duration: e.target.value })}
                  />
                )}
                <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                  <input
                    type="checkbox"
                    checked={s.continue_on_failure ?? false}
                    onChange={(e) => setStep(i, { ...s, continue_on_failure: e.target.checked })}
                  />
                  Carry on if it fails
                </label>
                <Button
                  type="button"
                  size="icon-sm"
                  variant="ghost"
                  aria-label="Remove the step"
                  className="ml-auto"
                  disabled={steps.length === 1}
                  onClick={() => setDef({ ...def, steps: steps.filter((_, j) => j !== i) })}
                >
                  <Trash2 />
                </Button>
              </div>
            ))}
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="self-start"
              disabled={steps.length >= 20}
              onClick={() =>
                setDef({ ...def, steps: [...steps, { type: "command", command: "" }] })
              }
            >
              <Plus /> Add a step
            </Button>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy}>
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
