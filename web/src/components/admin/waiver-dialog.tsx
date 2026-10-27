import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { cn } from "cn";
import { type FormEvent, useState } from "react";

import { Button } from "@/components/ui/button";
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
import type { SetBillingWaiverRequest, StaffWaiver } from "@/gen/raptor/panel/v1/staff_pb";
import { message } from "@/lib/errors";

type Waiver = Omit<SetBillingWaiverRequest, "$typeName" | "$unknown" | "orgId">;

// A date input's value (YYYY-MM-DD) for a timestamp, in local time.
function dateValue(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// WaiverDialog gives an org nodes free, and optionally its Backup Storage
// past what's included, or changes the waiver it has (DECISIONS #334).
export function WaiverDialog({
  org,
  nodes,
  current,
  onSave,
  onClose,
}: {
  org: string;
  nodes: number;
  current?: StaffWaiver;
  onSave: (w: Waiver) => Promise<unknown>;
  onClose: () => void;
}) {
  const [all, setAll] = useState(current?.allNodes ?? false);
  const [free, setFree] = useState(
    String(current && !current.allNodes ? current.freeNodes : Math.max(nodes, 1)),
  );
  const [storage, setStorage] = useState(current?.storage ?? false);
  const [until, setUntil] = useState(
    current?.endsAt ? dateValue(timestampDate(current.endsAt)) : "",
  );
  const [reason, setReason] = useState(current?.reason ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const count = Number.parseInt(free, 10) || 0;
  const valid = reason.trim() !== "" && (all || count > 0 || storage);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSave({
        allNodes: all,
        freeNodes: all ? 0 : count,
        storage,
        reason: reason.trim(),
        // Free through the whole of the day picked, in the staff member's
        // time.
        endsAt: until ? timestampFromDate(new Date(`${until}T23:59:59`)) : undefined,
      });
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const choice = (on: boolean) =>
    cn(
      "flex cursor-pointer flex-col gap-1 rounded-lg border p-3 transition-colors hover:bg-muted/50 has-focus-visible:ring-3 has-focus-visible:ring-ring/50",
      on && "border-primary bg-brand-wash hover:bg-brand-wash",
    );

  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {current ? "Change the waiver" : "Waive billing"} for {org}
          </DialogTitle>
          <DialogDescription>
            Free nodes aren't charged and need no plan. Its owners see what's free and until when on
            their Billing page, not why.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <fieldset className="flex flex-col gap-1.5">
            <legend className="mb-1.5 text-sm font-medium">Free nodes</legend>
            <div className="grid gap-2 sm:grid-cols-2">
              <label className={choice(!all)}>
                <input
                  type="radio"
                  name="waiver-nodes"
                  className="sr-only"
                  checked={!all}
                  onChange={() => setAll(false)}
                />
                <span className="text-sm font-medium">A number of them</span>
                <span className="text-xs text-muted-foreground">
                  The rest are charged. 0 for none.
                </span>
              </label>
              <label className={choice(all)}>
                <input
                  type="radio"
                  name="waiver-nodes"
                  className="sr-only"
                  checked={all}
                  onChange={() => setAll(true)}
                />
                <span className="text-sm font-medium">All of them</span>
                <span className="text-xs text-muted-foreground">
                  However many it links. It needs no plan.
                </span>
              </label>
            </div>
            {!all && (
              <Input
                aria-label="How many nodes are free"
                type="number"
                min={0}
                max={10000}
                className="w-32"
                value={free}
                onChange={(e) => setFree(e.target.value)}
              />
            )}
          </fieldset>
          <Label className="flex items-start gap-2 font-normal">
            <Checkbox
              className="mt-0.5"
              checked={storage}
              onCheckedChange={(on) => setStorage(on)}
            />
            <span>Raptor Backup Storage past the included 10 GB a node is free too</span>
          </Label>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="waiver-until">Free until (optional)</Label>
            <Input
              id="waiver-until"
              type="date"
              className="w-auto self-start"
              min={dateValue(new Date(Date.now() + 86_400_000))}
              value={until}
              onChange={(e) => setUntil(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Through that day. Owners are emailed 3 days before and when it ends. Empty: until it's
              ended here.
            </p>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="waiver-reason">Why</Label>
            <textarea
              id="waiver-reason"
              required
              maxLength={500}
              rows={3}
              className="rounded-lg border border-input bg-transparent px-2.5 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
              placeholder="Who they are, and the agreement or ticket"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Kept in the staff log. The org never sees it.
            </p>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !valid}>
              {current ? "Save" : "Waive"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
