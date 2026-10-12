import { type FormEvent, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { message } from "@/lib/errors";

// SuspendDialog asks why, then suspends: the reason goes in the staff log
// and the account's or org's own.
export function SuspendDialog({
  what,
  effect,
  onSuspend,
  onClose,
}: {
  what: string;
  effect: string;
  onSuspend: (reason: string) => Promise<unknown>;
  onClose: () => void;
}) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSuspend(reason.trim());
      onClose();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && !busy && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Suspend {what}?</DialogTitle>
          <DialogDescription>{effect}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="suspend-reason">Why</Label>
            <textarea
              id="suspend-reason"
              required
              maxLength={500}
              rows={3}
              className="rounded-lg border border-input bg-transparent px-2.5 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
              placeholder="The ticket or report, and what happened"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">Kept in the staff log and their own.</p>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="destructive" disabled={busy || !reason.trim()}>
              Suspend
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
