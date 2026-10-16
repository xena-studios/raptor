import { createFileRoute, Outlet } from "@tanstack/react-router";

import { requireSession } from "@/lib/session";

// Pop-outs (components/popout.tsx): one thing filling its own window, with
// no sidebar.
export const Route = createFileRoute("/popout")({
  beforeLoad: ({ location }) => requireSession(location),
  component: () => (
    <div className="flex h-dvh flex-col bg-background text-foreground">
      <Outlet />
    </div>
  ),
});
