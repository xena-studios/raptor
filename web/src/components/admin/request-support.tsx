import { type FormEvent, useState } from "react";
import { useReauth } from "@/components/reauth";
import { durationText, supportLevels } from "@/components/support-access";
import { Button } from "@/components/ui/button";
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
import { message } from "@/lib/errors";
import { staffClient } from "@/lib/transport";

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

// RequestSupport asks a node's owner for support access. They're emailed,
// and approve it (or less) with their passkey.
export function RequestSupport({
  nodeId,
  nodeName,
  onClose,
  onDone,
}: {
  nodeId: string;
  nodeName: string;
  onClose: () => void;
  onDone: () => void;
}) {
  const withReauth = useReauth();
  const [level, setLevel] = useState(1);
  const [minutes, setMinutes] = useState(60);
  const [reason, setReason] = useState("");
  const [ticket, setTicket] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await withReauth(() =>
        staffClient.requestSupportAccess({
          nodeId,
          level,
          minutes,
          reason: reason.trim(),
          ticket: ticket.trim(),
        }),
      );
      onDone();
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
          <DialogTitle>Ask for access to {nodeName}</DialogTitle>
          <DialogDescription>
            The org's owners and admins are emailed. Nothing happens until one approves it with
            their passkey, and they can give less than you ask for.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rq-level">Level</Label>
              <select
                id="rq-level"
                className={select}
                value={level}
                onChange={(e) => setLevel(Number(e.target.value))}
              >
                {[1, 2, 3].map((l) => (
                  <option key={l} value={l}>
                    {supportLevels[l]?.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="rq-time">For</Label>
              <select
                id="rq-time"
                className={select}
                value={minutes}
                onChange={(e) => setMinutes(Number(e.target.value))}
              >
                {[15, 60, 240, 1440, 4320, 10080].map((m) => (
                  <option key={m} value={m}>
                    {durationText(m)}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <p className="text-xs text-muted-foreground">Lets you {supportLevels[level]?.what}.</p>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rq-ticket">Ticket</Label>
            <Input
              id="rq-ticket"
              required
              maxLength={100}
              value={ticket}
              onChange={(e) => setTicket(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="rq-reason">Why (the owner reads this)</Label>
            <textarea
              id="rq-reason"
              required
              rows={3}
              maxLength={500}
              className="rounded-lg border border-input bg-transparent px-2.5 py-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={busy || !reason.trim() || !ticket.trim()}>
              Ask
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
