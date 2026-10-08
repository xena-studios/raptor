import { createFileRoute, Link } from "@tanstack/react-router";
import { CreditCard } from "lucide-react";

import { DetailList, EmptyState } from "@/components/page";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useOrgNodes } from "@/lib/org-data";

// What a node costs a month, once billing is on (docs/DECISIONS.md #14).
const nodePrice = 12;

export const Route = createFileRoute("/orgs/$orgId/settings/billing")({
  component: Billing,
});

// Billing isn't on yet: this says what the org would pay, so nobody is
// surprised when it is.
function Billing() {
  const { orgId } = Route.useParams();
  const nodes = useOrgNodes(orgId);
  const count = nodes.data?.nodes.length ?? 0;
  if (nodes.data && count === 0) {
    return (
      <div className="max-w-3xl">
        <EmptyState
          icon={CreditCard}
          title="No plan yet"
          description={`Your first node is free for a month; after that it's $${nodePrice} per node each month. Connect a node to get started: there's nothing to buy up front.`}
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
      </div>
    );
  }
  return (
    <div className="max-w-3xl space-y-6">
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
    </div>
  );
}
