import { TransportProvider } from "@connectrpc/connect-query";
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRouter, RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ConfirmProvider } from "@/components/confirm";
import { NotFound, RouteError } from "@/components/error-screen";
import { ReauthProvider } from "@/components/reauth";
import { ToastProvider } from "@/components/ui/toast";
import { TooltipProvider } from "@/components/ui/tooltip";
import { reportResult } from "@/lib/connection";
import { transport } from "@/lib/transport";
import { routeTree } from "@/routeTree.gen";
import "@/index.css";

// Every request's outcome says whether the Panel can be reached
// (lib/connection.ts). Data counts as fresh for 20 seconds, so going back to
// a page shows what it had without asking again; queries that poll keep
// their own interval, and changes invalidate what they touch.
const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 20_000 } },
  queryCache: new QueryCache({
    onError: (err) => reportResult(err),
    onSuccess: () => reportResult(undefined),
  }),
  mutationCache: new MutationCache({
    onError: (err) => reportResult(err),
    onSuccess: () => reportResult(undefined),
  }),
});
// Pointing at a link preloads its page's code and runs its route guards
// (the session is cached, lib/session.ts). React Query decides when data
// is fresh, so the router keeps no preload cache of its own.
const router = createRouter({
  routeTree,
  defaultPreload: "intent",
  defaultPreloadStaleTime: 0,
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
            <ToastProvider>
              <ConfirmProvider>
                <RouterProvider router={router} />
              </ConfirmProvider>
            </ToastProvider>
          </TooltipProvider>
        </ReauthProvider>
      </QueryClientProvider>
    </TransportProvider>
  </StrictMode>,
);
