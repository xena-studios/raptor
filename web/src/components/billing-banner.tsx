import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { Link } from "@tanstack/react-router";

import { BillingService } from "@/gen/raptor/panel/v1/billing_pb";
import { dateOnly } from "@/lib/format";

// BillingBanner says, on every page of an unpaid org, when its nodes go
// read-only, or that they are (docs/PANEL.md#billing-polar).
export function BillingBanner({ orgId }: { orgId: string }) {
  const billing = useQuery(BillingService.method.getBilling, { orgId }, { staleTime: 60_000 });
  const b = billing.data;
  if (!b?.enabled || !b.readOnlyAt) return null;
  return (
    <div
      role="status"
      className="border-b border-destructive/30 bg-destructive/5 px-6 py-2 text-sm text-destructive"
    >
      {b.readOnly
        ? "This org's nodes are read-only until its bill is paid. Game servers keep running. "
        : `This org needs a paid plan by ${dateOnly(timestampDate(b.readOnlyAt))}, or its nodes go read-only. `}
      <Link
        to="/orgs/$orgId/settings/billing"
        params={{ orgId }}
        className="font-medium underline underline-offset-3"
      >
        Billing
      </Link>
    </div>
  );
}
