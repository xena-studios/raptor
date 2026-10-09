import { createRootRoute, Outlet } from "@tanstack/react-router";

import { NotFound, RouteError } from "@/components/error-screen";

export const Route = createRootRoute({
  component: () => (
    <div className="min-h-screen bg-background text-foreground">
      <Outlet />
    </div>
  ),
  notFoundComponent: () => <NotFound />,
  errorComponent: ({ error, reset }) => <RouteError error={error} reset={reset} />,
});
