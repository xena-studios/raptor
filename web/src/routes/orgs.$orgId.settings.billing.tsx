import { type Timestamp, timestampDate } from "@bufbuild/protobuf/wkt";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { CreditCard, ExternalLink, Loader2 } from "lucide-react";
import { useState } from "react";

import { CancelPlan, Cards, Charges } from "@/components/billing/billing-portal";
import { PolarCheckout } from "@/components/billing/polar-embed";
import { DetailList, EmptyState } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { StatusBadge } from "@/components/server-status";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { BillingService, type GetBillingResponse } from "@/gen/raptor/panel/v1/billing_pb";
import { OrgService, Role } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { dateOnly } from "@/lib/format";
import { useIds } from "@/lib/ids";
import { useOrgNodes } from "@/lib/org-data";
import type { ServerStatus } from "@/lib/servers";
import { billingClient } from "@/lib/transport";

// What a node costs a month (docs/DECISIONS.md #14).
const nodePrice = 12;

export const Route = createFileRoute("/orgs/$orgId/settings/billing")({
  component: Billing,
  validateSearch: (s: Record<string, unknown>): { checkout?: "done" } =>
    s.checkout === "done" ? { checkout: "done" } : {},
});

function dollars(cents: bigint | number) {
  return `$${(Number(cents) / 100).toFixed(Number(cents) % 100 ? 2 : 0)}`;
}

function day(t?: Timestamp) {
  return t ? dateOnly(timestampDate(t)) : "—";
}

// The plan's state as a chip.
function planStatus(b: GetBillingResponse): ServerStatus {
  if (b.readOnly) return { label: "Read-only", tone: "failed" };
  switch (b.status) {
    case "trialing":
      return { label: "Free month", tone: "live" };
    case "active":
      return b.cancelAtPeriodEnd
        ? { label: "Ending", tone: "pending" }
        : { label: "Active", tone: "live" };
    case "past_due":
      return { label: "Payment failed", tone: "failed" };
    case "":
      return { label: "No plan", tone: "stopped" };
    default:
      return { label: "Ended", tone: "stopped" };
  }
}

function Billing() {
  const { orgId } = useIds();
  const { checkout } = Route.useSearch();
  const orgs = useQuery(OrgService.method.listOrgs, {});
  const owner = orgs.data?.orgs.find((o) => o.id === orgId)?.role === Role.OWNER;
  // Just back from Polar's checkout: the plan arrives by webhook in a
  // moment, so look again until it does.
  const billing = useQuery(
    BillingService.method.getBilling,
    { orgId },
    {
      refetchInterval: (q) =>
        checkout === "done" && !["trialing", "active"].includes(q.state.data?.status ?? "")
          ? 3000
          : false,
    },
  );
  const b = billing.data;
  if (billing.error) return <p className="text-sm text-destructive">{message(billing.error)}</p>;
  if (!b) return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (!b.enabled) return <NotCharging orgId={orgId} />;
  const live = ["trialing", "active", "past_due"].includes(b.status);
  return (
    <div className="space-y-6">
      {b.readOnlyAt && <Unpaid b={b} owner={owner} />}
      {b.waiver && <Courtesy b={b} />}
      {checkout === "done" && !live && (
        <p className="text-sm text-muted-foreground">
          <Loader2 className="mr-1.5 inline size-3.5 animate-spin" />
          Thanks. Your plan shows up here in a moment.
        </p>
      )}
      {live ? (
        <Plan orgId={orgId} b={b} owner={owner} />
      ) : (
        // Every node free, courtesy of Raptor: there's no plan to start.
        !b.waiver?.allNodes && <Start orgId={orgId} b={b} owner={owner} />
      )}
      {owner && live && <Cards orgId={orgId} b={b} />}
      {owner && b.status !== "" && <Charges orgId={orgId} />}
      {owner && b.status !== "" && <PolarPortal orgId={orgId} />}
    </div>
  );
}

// Courtesy says what Raptor gives the org free, and until when.
function Courtesy({ b }: { b: GetBillingResponse }) {
  const w = b.waiver;
  if (!w) return null;
  const nodes = w.allNodes
    ? "All your nodes are free"
    : w.freeNodes > 0
      ? `${w.freeNodes} ${w.freeNodes === 1 ? "node is" : "nodes are"} free`
      : "";
  const storage = "Raptor Backup Storage past what's included is free";
  const what = nodes
    ? `${nodes}${w.storage ? `, and ${storage.charAt(0).toLowerCase()}${storage.slice(1)}` : ""}`
    : storage;
  return (
    <div className="rounded-lg border border-primary/30 bg-brand-wash p-4 text-sm">
      <p className="font-medium">
        {what}, courtesy of Raptor{w.endsAt ? `, until ${day(w.endsAt)}` : ""}.
      </p>
      <p className="mt-1 text-muted-foreground">
        {w.allNodes
          ? "Your org needs no plan for them."
          : w.freeNodes > 0
            ? "They're not charged; a plan covers any others."
            : "Your nodes are charged as usual."}
        {w.endsAt ? " After that it's charged like any org. Owners are emailed 3 days before." : ""}
      </p>
    </div>
  );
}

function Unpaid({ b, owner }: { b: GetBillingResponse; owner: boolean }) {
  return (
    <div className="rounded-lg border border-destructive/40 bg-destructive/5 p-4 text-sm">
      <p className="font-medium text-destructive">
        {b.readOnly
          ? "This org's nodes are read-only until it's paid."
          : `This org needs a paid plan by ${day(b.readOnlyAt)}.`}
      </p>
      <p className="mt-1 text-muted-foreground">
        {b.readOnly
          ? "Nothing on them can be changed from Raptor. Game servers keep running."
          : "After that its nodes are read-only in Raptor until it's paid. Game servers keep running either way."}{" "}
        {owner
          ? b.status === "past_due"
            ? "Add a card below: it's charged right away."
            : "Start a plan below, or remove nodes you don't use."
          : "An owner of the org can fix it here."}
      </p>
    </div>
  );
}

function Start({ orgId, b, owner }: { orgId: string; b: GetBillingResponse; owner: boolean }) {
  const start = useMutation(BillingService.method.startCheckout);
  const navigate = useNavigate();
  const [checkout, setCheckout] = useState<{ url: string; origin: string } | null>(null);
  const [error, setError] = useState("");
  const price = dollars(b.nodePriceCents);
  const charged = b.nodes - b.freeNodes;
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-xs space-y-1.5">
          <CardTitle>Plan</CardTitle>
          <CardDescription>
            {price} per linked node each month, prorated by the day.
            {b.trialAvailable
              ? " Your first node is free for its first month. A card is needed to start; nothing is charged until the free month ends."
              : ""}
            {b.freeNodes > 0 && charged === 0
              ? " Your free nodes need no plan: start one before linking more."
              : ""}
          </CardDescription>
        </div>
        <StatusBadge status={planStatus(b)} />
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DetailList
          rows={[
            ["Nodes", String(b.nodes)],
            ...(b.freeNodes > 0
              ? ([["Free, courtesy of Raptor", String(b.freeNodes)]] as [string, string][])
              : []),
            ["Would be", `${dollars(BigInt(Math.max(charged, 1)) * b.nodePriceCents)} / month`],
          ]}
        />
        {owner ? (
          <div>
            <Button
              disabled={start.isPending || checkout !== null}
              onClick={async () => {
                setError("");
                try {
                  const res = await start.mutateAsync({ orgId });
                  setCheckout({ url: res.url, origin: res.polarOrigin });
                } catch (err) {
                  setError(message(err));
                }
              }}
            >
              {start.isPending || checkout ? <Loader2 className="animate-spin" /> : <CreditCard />}
              {b.trialAvailable ? "Start your free month" : "Start a plan"}
            </Button>
            <p className="mt-2 text-xs text-muted-foreground">
              Payment is handled by Polar, Raptor's merchant of record. It adds sales tax or VAT
              where it's due.
            </p>
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">An owner of the org starts the plan.</p>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      {checkout && (
        <PolarCheckout
          url={checkout.url}
          origin={checkout.origin}
          onSuccess={() => {
            setCheckout(null);
            void navigate({ to: ".", search: { checkout: "done" }, replace: true });
          }}
          onClose={() => setCheckout(null)}
          // It didn't load here: Polar's own page works anywhere.
          onFallback={() => window.location.assign(checkout.url)}
        />
      )}
    </Card>
  );
}

function Plan({ orgId, b, owner }: { orgId: string; b: GetBillingResponse; owner: boolean }) {
  const client = useQueryClient();
  const withReauth = useReauth();
  const endTrial = useMutation(BillingService.method.endTrial);
  const [cancelling, setCancelling] = useState(false);
  const [resuming, setResuming] = useState(false);
  const [error, setError] = useState("");
  const monthly = BigInt(b.seats) * b.nodePriceCents;
  const charged = b.nodes - b.freeNodes;
  const rows: [string, string][] = [["Nodes", String(b.nodes)]];
  if (b.freeNodes > 0) rows.push(["Free, courtesy of Raptor", String(b.freeNodes)]);
  rows.push(["Price per node", `${dollars(b.nodePriceCents)} / month`]);
  if (b.status === "trialing") {
    rows.push(
      [b.cancelAtPeriodEnd ? "Ends" : "Free until", day(b.trialEnd)],
      ["Then", b.cancelAtPeriodEnd ? "Nothing" : `${dollars(monthly)} / month`],
    );
  } else {
    rows.push(
      ["Monthly", `${dollars(monthly)}`],
      [b.cancelAtPeriodEnd ? "Ends" : "Renews", day(b.currentPeriodEnd)],
    );
  }
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-xs space-y-1.5">
          <CardTitle>Plan</CardTitle>
          <CardDescription>
            {b.cancelAtPeriodEnd
              ? b.nodes === 0
                ? "This plan ends with the period, since the org has no nodes. Connect one and it carries on."
                : charged === 0
                  ? "This plan ends with the period, since Raptor makes all your nodes free. It carries on if any are charged again."
                  : "This plan ends with the period. Nodes still linked then make the org unpaid."
              : b.status === "trialing"
                ? "Your first node is free this month. Linking a second ends the free month, and both are charged from then."
                : "Every linked node is charged, online or not. Adding or removing one changes the bill by the day."}
          </CardDescription>
        </div>
        <StatusBadge status={planStatus(b)} />
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <DetailList rows={rows} />
        {owner && (
          <div className="flex flex-wrap gap-2">
            {b.status === "trialing" && !b.cancelAtPeriodEnd && (
              <Button
                variant="outline"
                disabled={endTrial.isPending}
                onClick={async () => {
                  setError("");
                  try {
                    await endTrial.mutateAsync({ orgId });
                    await client.invalidateQueries();
                  } catch (err) {
                    setError(message(err));
                  }
                }}
              >
                End the free month now
              </Button>
            )}
            {b.cancelAtPeriodEnd
              ? charged > 0 && (
                  <Button
                    disabled={resuming}
                    onClick={async () => {
                      setResuming(true);
                      setError("");
                      try {
                        await withReauth(() => billingClient.cancelPlan({ orgId, resume: true }));
                        await client.invalidateQueries();
                      } catch (err) {
                        setError(message(err));
                      } finally {
                        setResuming(false);
                      }
                    }}
                  >
                    {resuming && <Loader2 className="animate-spin" />}
                    Keep the plan
                  </Button>
                )
              : b.status !== "past_due" && (
                  <Button variant="ghost" onClick={() => setCancelling(true)}>
                    Cancel plan
                  </Button>
                )}
          </div>
        )}
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
      <CancelPlan orgId={orgId} b={b} open={cancelling} onOpenChange={setCancelling} />
    </Card>
  );
}

// PolarPortal links to Polar's own portal, for what the page doesn't do.
function PolarPortal({ orgId }: { orgId: string }) {
  const portal = useMutation(BillingService.method.openBillingPortal);
  const [error, setError] = useState("");
  return (
    <p className="text-sm text-muted-foreground">
      Billing address, tax ID, and the email receipts go to are in{" "}
      <button
        type="button"
        className="inline-flex items-center gap-1 text-primary underline-offset-4 hover:underline disabled:opacity-50"
        disabled={portal.isPending}
        onClick={async () => {
          setError("");
          try {
            const res = await portal.mutateAsync({ orgId });
            window.location.assign(res.url);
          } catch (err) {
            setError(message(err));
          }
        }}
      >
        Polar's portal
        {portal.isPending ? (
          <Loader2 className="size-3.5 animate-spin" />
        ) : (
          <ExternalLink className="size-3.5" />
        )}
      </button>
      .{error && <span className="block text-destructive">{error}</span>}
    </p>
  );
}

// NotCharging: billing isn't on, so this says what the org would pay.
function NotCharging({ orgId }: { orgId: string }) {
  const nodes = useOrgNodes(orgId);
  const count = nodes.data?.nodes.length ?? 0;
  if (nodes.data && count === 0) {
    return (
      <EmptyState
        icon={CreditCard}
        title="No plan yet"
        description={`Raptor isn't charging yet. When it starts, every linked node costs $${nodePrice} a month, and an org's first node is free for its first month.`}
        action={
          <Link
            to="/orgs/$orgId/nodes/new"
            params={{ orgId }}
            className={buttonVariants({ size: "sm" })}
          >
            Connect node
          </Link>
        }
      />
    );
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>Plan</CardTitle>
        <CardDescription>
          Raptor isn't charging yet. When it starts, every linked node costs ${nodePrice} a month,
          prorated by the day, and an org's first node is free for its first month.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <DetailList
          rows={[
            ["Nodes", String(count)],
            ["Price per node", `$${nodePrice} / month`],
            ["Would be", `$${count * nodePrice} / month`],
          ]}
        />
      </CardContent>
    </Card>
  );
}
