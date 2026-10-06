import { createFileRoute } from "@tanstack/react-router";

import { AppShell } from "@/components/app-shell";
import { requireSession } from "@/lib/session";

export const Route = createFileRoute("/")({
  beforeLoad: ({ location }) => requireSession(location),
  component: Home,
});

function Home() {
  const session = Route.useRouteContext();
  return (
    <AppShell session={session}>
      <h1 className="font-heading text-2xl font-semibold">Orgs</h1>
    </AppShell>
  );
}
