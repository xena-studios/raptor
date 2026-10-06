import { useMutation } from "@connectrpc/connect-query";
import { createFileRoute, useNavigate } from "@tanstack/react-router";

import { Button } from "@/components/ui/button";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { requireSession } from "@/lib/session";

export const Route = createFileRoute("/")({
  beforeLoad: ({ location }) => requireSession(location),
  component: Home,
});

function Home() {
  const session = Route.useRouteContext();
  const navigate = useNavigate();
  const signOut = useMutation(AuthService.method.signOut);

  return (
    <main className="mx-auto max-w-3xl p-8">
      <header className="flex items-center justify-between">
        <h1 className="font-heading text-2xl font-semibold">Raptor</h1>
        <div className="flex items-center gap-3 text-sm">
          <span className="text-muted-foreground">{session.user?.email}</span>
          <Button
            variant="outline"
            size="sm"
            disabled={signOut.isPending}
            onClick={async () => {
              await signOut.mutateAsync({});
              await navigate({ to: "/signin" });
            }}
          >
            Sign out
          </Button>
        </div>
      </header>
    </main>
  );
}
