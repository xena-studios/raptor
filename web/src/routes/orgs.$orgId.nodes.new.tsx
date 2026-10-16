import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import { CreditCard } from "lucide-react";

import { ConnectNode } from "@/components/org/connect-node";
import { EmptyState, PageHeader } from "@/components/page";
import { buttonVariants } from "@/components/ui/button";
import { BillingService } from "@/gen/raptor/panel/v1/billing_pb";
import { useIds } from "@/lib/ids";

export const Route = createFileRoute("/orgs/$orgId/nodes/new")({
  component: NewNode,
});

function NewNode() {
  const session = Route.useRouteContext();
  const { orgId } = useIds();
  // With billing on, a node needs a plan; a second ends the free month.
  const billing = useQuery(BillingService.method.getBilling, { orgId });
  const b = billing.data;
  const needsPlan =
    b?.enabled && (!["trialing", "active", "past_due"].includes(b.status) || b.readOnly);
  return (
    <>
      <PageHeader
        back={{ label: "Nodes", to: "/orgs/$orgId/nodes", params: { orgId } }}
        title="Connect a node"
        description="Install Raptor on a machine of yours and link it to this org."
      />
      {!b ? null : needsPlan ? (
        <EmptyState
          icon={CreditCard}
          title={b.readOnly ? "This org's bill needs paying first" : "Start a plan first"}
          description={
            b.readOnly
              ? "Its nodes are read-only until it's paid. An owner can fix it in Billing."
              : b.trialAvailable
                ? "Your first node is free for its first month. A card is needed to start; nothing is charged until the month ends."
                : "Every linked node is $12 a month, prorated by the day."
          }
          action={
            <Link
              to="/orgs/$orgId/settings/billing"
              params={{ orgId }}
              className={buttonVariants({ size: "sm" })}
            >
              Go to billing
            </Link>
          }
        />
      ) : (
        <>
          {b.status === "trialing" && b.nodes >= 1 && (
            <p className="mb-4 rounded-lg border border-warn/40 bg-warn/5 p-3 text-sm">
              Your free month covers one node. Linking this one ends it, and both nodes are charged
              from then.
            </p>
          )}
          <ConnectNode orgId={orgId} userId={session.user?.id ?? ""} />
        </>
      )}
    </>
  );
}
