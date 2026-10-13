import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient, useQuery as useTanQuery } from "@tanstack/react-query";
import { Headset, Loader2 } from "lucide-react";
import { useState } from "react";

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
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import type { SupportRequest } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";
import { dateTime, when } from "@/lib/format";
import { sendSigned } from "@/lib/signed";
import { commandClient, orgClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

// Support access, the owner's side (docs/PANEL.md#support-access): Raptor's
// staff ask; you approve with your passkey (the node keeps what you sign,
// and checks every staff command against it), lower it, deny it, or end it.

export const supportLevels: Record<number, { name: string; what: string }> = {
  1: { name: "Diagnostics", what: "look at the node's health, checks, metrics, and Wings' log" },
  2: { name: "Operate", what: "that, and start, stop, and restart servers and use their consoles" },
  3: { name: "Manage", what: "that, and read and change servers' files, schedules, and backups" },
};

const durations = [15, 60, 4 * 60, 24 * 60, 3 * 24 * 60, 7 * 24 * 60];

export function durationText(minutes: number): string {
  if (minutes % (24 * 60) === 0)
    return minutes === 24 * 60 ? "1 day" : `${minutes / (24 * 60)} days`;
  if (minutes % 60 === 0) return minutes === 60 ? "1 hour" : `${minutes / 60} hours`;
  return `${minutes} minutes`;
}

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// SupportBanner shows requests waiting for an answer and access in force,
// on every page of the node (admins and owners).
export function SupportBanner({
  orgId,
  nodeId,
  userId,
}: {
  orgId: string;
  nodeId: string;
  userId: string;
}) {
  const list = useQuery(
    OrgService.method.listNodeSupportRequests,
    { orgId, nodeId },
    { refetchInterval: 60_000 },
  );
  const client = useQueryClient();
  const [review, setReview] = useState<SupportRequest>();
  const [error, setError] = useState("");
  const pending = (list.data?.requests ?? []).filter((r) => r.status === "pending");
  const active = (list.data?.requests ?? []).filter((r) => r.status === "approved");
  if (pending.length === 0 && active.length === 0) return null;
  return (
    <div className="flex flex-col gap-2">
      {pending.map((r) => (
        <div
          key={r.id}
          className="flex flex-wrap items-center gap-3 rounded-lg border border-warn/40 bg-warn-wash p-3 text-sm"
        >
          <Headset className="size-4 shrink-0 text-warn" />
          <span className="min-w-0 flex-1">
            <span className="font-medium">Raptor support ({r.staffEmail})</span> asks for{" "}
            {supportLevels[r.level]?.name.toLowerCase()} access for {durationText(r.minutes)}:{" "}
            {r.reason} <span className="text-muted-foreground">({r.ticket})</span>
          </span>
          <Button size="sm" onClick={() => setReview(r)}>
            Review
          </Button>
        </div>
      ))}
      {active.map((r) => (
        <div
          key={r.id}
          className="flex flex-wrap items-center gap-3 rounded-lg border border-[var(--series-1)]/40 bg-[var(--series-1)]/5 p-3 text-sm"
        >
          <Headset className="size-4 shrink-0 text-[var(--series-1)]" />
          <span className="min-w-0 flex-1">
            <span className="font-medium">Raptor support ({r.staffEmail})</span> has{" "}
            {supportLevels[r.grantedLevel]?.name.toLowerCase()} access until {when(r.expiresAt)}.
            Everything they do is in the org's activity.
          </span>
          <Button
            size="sm"
            variant="outline"
            onClick={async () => {
              setError("");
              try {
                await commandClient.execute({
                  nodeId,
                  action: "support.revoke",
                  paramsJson: JSON.stringify({ id: r.id }),
                });
                await client.invalidateQueries();
              } catch (err) {
                setError(message(err));
              }
            }}
          >
            End now
          </Button>
        </div>
      ))}
      {error && <p className="text-sm text-destructive">{error}</p>}
      {review && (
        <ReviewDialog
          orgId={orgId}
          nodeId={nodeId}
          userId={userId}
          r={review}
          onClose={() => setReview(undefined)}
        />
      )}
    </div>
  );
}

function ReviewDialog({
  orgId,
  nodeId,
  userId,
  r,
  onClose,
}: {
  orgId: string;
  nodeId: string;
  userId: string;
  r: SupportRequest;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const [level, setLevel] = useState(r.level);
  const [minutes, setMinutes] = useState(r.minutes);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");

  async function approve() {
    setBusy("approve");
    setError("");
    try {
      await sendSigned({
        userId,
        nodeId,
        action: "support.grant",
        params: {
          id: r.id,
          staff_id: r.staffId,
          staff_email: r.staffEmail,
          level,
          reason: r.reason,
          ticket: r.ticket,
          expires_at: Date.now() + minutes * 60_000,
        },
      });
      await client.invalidateQueries();
      onClose();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy("");
    }
  }

  async function deny() {
    setBusy("deny");
    setError("");
    try {
      await orgClient.denySupportRequest({ orgId, requestId: r.id });
      await client.invalidateQueries();
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Raptor support asks for access</DialogTitle>
          <DialogDescription>
            {r.staffEmail}, for ticket {r.ticket}: {r.reason}
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4 text-sm">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="sup-level">What they can do</Label>
            <select
              id="sup-level"
              className={select}
              value={level}
              onChange={(e) => setLevel(Number(e.target.value))}
            >
              {[1, 2, 3]
                .filter((l) => l <= r.level)
                .map((l) => (
                  <option key={l} value={l}>
                    {supportLevels[l]?.name}
                  </option>
                ))}
            </select>
            <p className="text-xs text-muted-foreground">
              They can {supportLevels[level]?.what}. Never anything that needs your passkey, and no
              shell.
            </p>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="sup-time">For</Label>
            <select
              id="sup-time"
              className={select}
              value={minutes}
              onChange={(e) => setMinutes(Number(e.target.value))}
            >
              {[...new Set([...durations.filter((d) => d < r.minutes), r.minutes])].map((d) => (
                <option key={d} value={d}>
                  {durationText(d)}
                </option>
              ))}
            </select>
          </div>
          <p className="text-muted-foreground">
            Your passkey signs exactly this, and the node checks every command they send against it.
            You can end it any time here, or with sudo raptor support revoke on the node.
          </p>
          {error && <p className="text-destructive">{error}</p>}
        </div>
        <DialogFooter className="sm:justify-between">
          <Button variant="outline" className="text-destructive" disabled={!!busy} onClick={deny}>
            {busy === "deny" && <Loader2 className="animate-spin" />} Deny
          </Button>
          <Button disabled={!!busy} onClick={approve}>
            {busy === "approve" && <Loader2 className="animate-spin" />} Approve with passkey
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// SupportSettings is the node's support access switch and history, from
// the node itself.
export function SupportSettings({ nodeId, connected }: { nodeId: string; connected: boolean }) {
  const client = useQueryClient();
  const state = useTanQuery({
    queryKey: ["support-list", nodeId],
    queryFn: async () => {
      const res = await commandClient.execute({ nodeId, action: "support.list", paramsJson: "{}" });
      return JSON.parse(res.resultJson) as {
        disabled: boolean;
        grants:
          | {
              id: string;
              staff_email: string;
              level: number;
              expires_at: number;
              revoked_at?: number;
            }[]
          | null;
      };
    },
    enabled: connected,
  });
  const [error, setError] = useState("");
  const now = Date.now();
  return (
    <Card>
      <CardHeader>
        <CardTitle>Support access</CardTitle>
        <CardDescription>
          Raptor's staff can ask for access to help with a ticket; you approve each time with your
          passkey. Turning it off ends any access and refuses new requests on the node itself.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {!connected ? (
          <p className="text-sm text-muted-foreground">The node is offline.</p>
        ) : state.data ? (
          <>
            <div className="flex items-center justify-between gap-4">
              <label htmlFor="support-on" className="block text-sm font-medium">
                Allow support access
              </label>
              <Switch
                id="support-on"
                checked={!state.data.disabled}
                onCheckedChange={async (on) => {
                  setError("");
                  try {
                    await commandClient.execute({
                      nodeId,
                      action: "support.disable",
                      paramsJson: JSON.stringify({ off: !on }),
                    });
                    await client.invalidateQueries();
                  } catch (err) {
                    setError(message(err));
                  }
                }}
              />
            </div>
            {(state.data.grants ?? []).length > 0 && (
              <ul className="divide-y text-sm">
                {(state.data.grants ?? []).map((g) => {
                  const live = !g.revoked_at && g.expires_at > now;
                  return (
                    <li key={g.id} className="flex flex-wrap justify-between gap-x-4 py-2">
                      <span>
                        {g.staff_email} · {supportLevels[g.level]?.name}
                      </span>
                      <span className="text-muted-foreground">
                        {live
                          ? `until ${dateTime(new Date(g.expires_at))}`
                          : g.revoked_at
                            ? `ended ${dateTime(new Date(g.revoked_at))}`
                            : `ran out ${dateTime(new Date(g.expires_at))}`}
                      </span>
                    </li>
                  );
                })}
              </ul>
            )}
          </>
        ) : (
          <p className="text-sm text-muted-foreground">
            {state.error ? message(state.error) : "Asking the node…"}
          </p>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}
