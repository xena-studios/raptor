import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { CreditCard, ExternalLink, FileText, Loader2, Plus } from "lucide-react";
import { type FormEvent, useState } from "react";

import { PolarCardForm } from "@/components/billing/polar-embed";
import { useReauth } from "@/components/reauth";
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
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { toast } from "@/components/ui/toast";
import {
  BillingService,
  type Charge,
  type GetBillingResponse,
  type Card as SavedCard,
} from "@/gen/raptor/panel/v1/billing_pb";
import { message } from "@/lib/errors";
import { dateOnly } from "@/lib/format";
import { billingClient } from "@/lib/transport";

// The billing page's own portal, through the Panel (docs/PANEL.md#billing-polar):
// the cards Polar charges, the charges it made, and cancelling. What's left
// (billing address, tax ID, the email receipts go to) stays in Polar's
// portal, linked at the bottom.

const select =
  "h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";
const textarea =
  "w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 dark:bg-input/30";

function money(cents: bigint, currency: string) {
  try {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency: currency.toUpperCase() || "USD",
    }).format(Number(cents) / 100);
  } catch {
    return `${(Number(cents) / 100).toFixed(2)} ${currency.toUpperCase()}`;
  }
}

function brandName(c: SavedCard) {
  if (c.wallet === "apple_pay") return "Apple Pay";
  if (c.wallet === "google_pay") return "Google Pay";
  if (c.type !== "card" && c.type !== "kr_card") return c.type.replaceAll("_", " ");
  const b = c.brand || "Card";
  return b === "amex" ? "Amex" : b.charAt(0).toUpperCase() + b.slice(1);
}

// refresh reads billing again now, and once more shortly after: Polar
// takes a moment to show a card it just saved.
function useRefresh() {
  const client = useQueryClient();
  return () => {
    void client.invalidateQueries();
    setTimeout(() => void client.invalidateQueries(), 3000);
  };
}

export function Cards({ orgId, b }: { orgId: string; b: GetBillingResponse }) {
  const details = useQuery(BillingService.method.getBillingDetails, { orgId });
  const withReauth = useReauth();
  const refresh = useRefresh();
  const [adding, setAdding] = useState<{ token: string; origin: string } | null>(null);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const cards = details.data?.cards ?? [];

  async function run(id: string, change: () => Promise<unknown>) {
    setBusy(id);
    setError("");
    try {
      await withReauth(change);
      refresh();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  async function add() {
    setBusy("add");
    setError("");
    try {
      const res = await withReauth(() => billingClient.startAddCard({ orgId }));
      setAdding({ token: res.sessionToken, origin: res.polarOrigin });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Payment method</CardTitle>
        <CardDescription>
          {b.status === "past_due"
            ? "The last payment failed. Add a card: it becomes the default and is charged right away."
            : b.status === "trialing"
              ? "The default card is charged when the free month ends."
              : "The plan is charged to the default card each month."}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {details.error ? (
          <p className="text-sm text-destructive">{message(details.error)}</p>
        ) : !details.data ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : cards.length === 0 ? (
          <p className="text-sm text-muted-foreground">No card saved.</p>
        ) : (
          <ul className="divide-y text-sm">
            {cards.map((c) => (
              <li key={c.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 py-2.5">
                <CreditCard className="size-4 text-muted-foreground" aria-hidden />
                <span className="font-medium">{brandName(c)}</span>
                {c.last4 && <span className="font-mono tabular-nums">•••• {c.last4}</span>}
                {c.expYear > 0 && (
                  <span className="text-muted-foreground tabular-nums">
                    Expires {String(c.expMonth).padStart(2, "0")}/{String(c.expYear).slice(-2)}
                  </span>
                )}
                {c.default && <Badge>Default</Badge>}
                {!c.default && (
                  <span className="ml-auto flex gap-1">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={busy !== ""}
                      onClick={() =>
                        run(c.id, () => billingClient.setDefaultCard({ orgId, cardId: c.id }))
                      }
                    >
                      {busy === c.id && <Loader2 className="animate-spin" />}
                      Make default
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={busy !== ""}
                      onClick={() =>
                        run(c.id, () => billingClient.removeCard({ orgId, cardId: c.id }))
                      }
                    >
                      Remove
                    </Button>
                  </span>
                )}
              </li>
            ))}
          </ul>
        )}
        {adding ? (
          <div className="flex flex-col gap-2 rounded-lg border p-3">
            <PolarCardForm
              token={adding.token}
              origin={adding.origin}
              onSuccess={() => {
                setAdding(null);
                toast.success("Card added", "It's the default now.");
                refresh();
              }}
              onError={(code) =>
                setError(
                  code === "unauthorized"
                    ? "The form timed out. Close it and add the card again."
                    : "The card couldn't be saved. Check its details, or try another.",
                )
              }
            />
            <div>
              <Button size="sm" variant="ghost" onClick={() => setAdding(null)}>
                Close
              </Button>
            </div>
          </div>
        ) : (
          <div>
            <Button variant="outline" disabled={busy !== ""} onClick={add}>
              {busy === "add" ? <Loader2 className="animate-spin" /> : <Plus />}
              Add a card
            </Button>
          </div>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}

const reasonNames: Record<string, string> = {
  subscription_create: "First payment",
  subscription_cycle: "Monthly",
  subscription_update: "Node change",
  subscription_meter_cycle: "Usage",
  purchase: "Payment",
};

const chargeStatus: Record<string, string> = {
  paid: "Paid",
  pending: "Due",
  draft: "Draft",
  refunded: "Refunded",
  partially_refunded: "Part refunded",
  void: "Void",
};

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

export function Charges({ orgId }: { orgId: string }) {
  const details = useQuery(BillingService.method.getBillingDetails, { orgId });
  const charges = details.data?.charges ?? [];
  return (
    <Card>
      <CardHeader>
        <CardTitle>Charges</CardTitle>
        <CardDescription>
          The last 24, with their invoices. Polar adds sales tax or VAT where it's due.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {details.error ? (
          <p className="text-sm text-destructive">{message(details.error)}</p>
        ) : !details.data ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : charges.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing charged yet.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Date</TableHead>
                <TableHead>For</TableHead>
                <TableHead className="text-right">Amount</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>
                  <span className="sr-only">Invoice</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {charges.map((c) => (
                <ChargeRow key={c.id} orgId={orgId} c={c} />
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

function ChargeRow({ orgId, c }: { orgId: string; c: Charge }) {
  const [busy, setBusy] = useState(false);
  const [url, setUrl] = useState("");
  const [error, setError] = useState("");
  const refunded = c.refundedCents > 0n;

  // The first time, Polar makes the invoice, which takes a few seconds.
  async function invoice() {
    setBusy(true);
    setError("");
    try {
      for (let i = 0; ; i++) {
        try {
          const res = await billingClient.getInvoice({ orgId, chargeId: c.id });
          setUrl(res.url);
          window.open(res.url, "_blank", "noopener");
          return;
        } catch (err) {
          const pending = err instanceof ConnectError && err.code === Code.Unavailable;
          if (!pending || i >= 7) throw err;
          await sleep(2000);
        }
      }
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <TableRow>
      <TableCell className="tabular-nums">{c.at ? dateOnly(timestampDate(c.at)) : "—"}</TableCell>
      <TableCell>
        {reasonNames[c.reason] ?? "Payment"}
        {c.invoiceNumber && (
          <span className="ml-2 font-mono text-xs text-muted-foreground">{c.invoiceNumber}</span>
        )}
      </TableCell>
      <TableCell className="text-right tabular-nums">
        {money(c.totalCents, c.currency)}
        {refunded && c.refundedCents < c.totalCents && (
          <span className="block text-xs text-muted-foreground">
            {money(c.refundedCents, c.currency)} back
          </span>
        )}
      </TableCell>
      <TableCell>
        <span className={c.status === "pending" ? "text-destructive" : undefined}>
          {chargeStatus[c.status] ?? c.status}
        </span>
      </TableCell>
      <TableCell className="text-right">
        {url ? (
          <a
            href={url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-sm text-primary underline-offset-4 hover:underline"
          >
            <ExternalLink className="size-3.5" />
            Open
          </a>
        ) : (
          c.status !== "draft" &&
          c.status !== "void" && (
            <Button size="sm" variant="ghost" disabled={busy} onClick={invoice}>
              {busy ? <Loader2 className="animate-spin" /> : <FileText />}
              Invoice
            </Button>
          )
        )}
        {error && <span className="block text-xs text-destructive">{error}</span>}
      </TableCell>
    </TableRow>
  );
}

// Polar's reasons, in the owner's words.
const reasons: [string, string][] = [
  ["too_expensive", "It costs too much"],
  ["unused", "I don't use it enough"],
  ["missing_features", "It's missing something I need"],
  ["switched_service", "I'm moving to something else"],
  ["low_quality", "It isn't reliable enough"],
  ["too_complex", "It's too hard to use"],
  ["customer_service", "Support let me down"],
  ["other", "Something else"],
];

// CancelPlan ends the plan with its period, after asking why (optional).
export function CancelPlan({
  orgId,
  b,
  open,
  onOpenChange,
}: {
  orgId: string;
  b: GetBillingResponse;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const withReauth = useReauth();
  const client = useQueryClient();
  const [reason, setReason] = useState("");
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const trial = b.status === "trialing";
  const ends = trial ? b.trialEnd : b.currentPeriodEnd;
  const day = ends ? dateOnly(timestampDate(ends)) : "the end of the period";

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await withReauth(() => billingClient.cancelPlan({ orgId, reason, comment }));
      await client.invalidateQueries();
      onOpenChange(false);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        onOpenChange(next);
        setError("");
      }}
    >
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Cancel the plan?</DialogTitle>
            <DialogDescription>
              {trial
                ? `It ends with the free month, on ${day}, and nothing is charged.`
                : `It ends on ${day}, and nothing more is charged.`}{" "}
              Until then everything works. Nodes still linked after that make the org unpaid, and 7
              days later they're read-only in Raptor. Game servers keep running either way.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cancel-reason">Why are you cancelling? (optional)</Label>
            <select
              id="cancel-reason"
              className={select}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            >
              <option value="">Rather not say</option>
              {reasons.map(([v, label]) => (
                <option key={v} value={v}>
                  {label}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="cancel-comment">Anything we should know? (optional)</Label>
            <textarea
              id="cancel-comment"
              rows={3}
              maxLength={2000}
              className={textarea}
              value={comment}
              onChange={(e) => setComment(e.target.value)}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Keep the plan
            </Button>
            <Button type="submit" variant="destructive" disabled={busy}>
              {busy && <Loader2 className="animate-spin" />}
              Cancel plan
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
