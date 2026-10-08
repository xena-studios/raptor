import { createFileRoute } from "@tanstack/react-router";

import { Authenticator, Devices, Passkeys, SSHKeys } from "@/components/account/security";

export const Route = createFileRoute("/settings/security")({
  component: Security,
});

function Security() {
  return (
    <div className="max-w-2xl space-y-6">
      <Passkeys />
      <Authenticator />
      <SSHKeys />
      <Devices />
    </div>
  );
}
