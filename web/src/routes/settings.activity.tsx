import { createFileRoute } from "@tanstack/react-router";

import { Activity } from "@/components/account/security";

export const Route = createFileRoute("/settings/activity")({
  component: AccountActivity,
});

function AccountActivity() {
  return (
    <div className="max-w-3xl">
      <Activity />
    </div>
  );
}
