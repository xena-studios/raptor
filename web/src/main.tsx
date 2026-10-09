import { TransportProvider } from "@connectrpc/connect-query";
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { NotFound, RouteError } from "@/components/error-screen";
import { ReauthProvider } from "@/components/reauth";
import { TooltipProvider } from "@/components/ui/tooltip";
import { reportResult } from "@/lib/connection";
import { transport } from "@/lib/transport";
import { routeTree } from "@/routeTree.gen";
import "@/index.css";

// Every request's outcome says whether the Panel can be reached
// (lib/connection.ts).
const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (err) => reportResult(err),
    onSuccess: () => reportResult(undefined),
  }),
  mutationCache: new MutationCache({
    onError: (err) => reportResult(err),
    onSuccess: () => reportResult(undefined),
  }),
});
const router = createRouter({
  routeTree,
  defaultNotFoundComponent: () => <NotFound />,
  defaultErrorComponent: ({ error, reset }) => <RouteError error={error} reset={reset} />,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

const root = document.getElementById("root");
if (!root) throw new Error("missing #root element");

createRoot(root).render(
  <StrictMode>
    <TransportProvider transport={transport}>
      <QueryClientProvider client={queryClient}>
        <ReauthProvider>
          <TooltipProvider>
            <RouterProvider router={router} />
          </TooltipProvider>
        </ReauthProvider>
      </QueryClientProvider>
    </TransportProvider>
  </StrictMode>,
);
