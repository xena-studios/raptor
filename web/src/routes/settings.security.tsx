import { createFileRoute } from "@tanstack/react-router";

import { Authenticator, Devices, Passkeys } from "@/components/account/security";

export const Route = createFileRoute("/settings/security")({
  component: Security,
});

function Security() {
  return (
    <div className="space-y-6">
      <Passkeys />
      <Authenticator />
      <Devices />
    </div>
  );
}
