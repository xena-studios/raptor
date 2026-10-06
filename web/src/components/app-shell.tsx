import { useMutation } from "@connectrpc/connect-query";
import { Link, useNavigate } from "@tanstack/react-router";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { AuthService, type GetSessionResponse } from "@/gen/raptor/panel/v1/auth_pb";

// The frame around signed-in pages.
export function AppShell({
  session,
  children,
}: {
  session: GetSessionResponse;
  children: ReactNode;
}) {
  const navigate = useNavigate();
  const signOut = useMutation(AuthService.method.signOut);
  const link = "text-muted-foreground hover:text-foreground [&.active]:text-foreground";
  return (
    <div className="mx-auto max-w-4xl p-6">
      <header className="mb-8 flex items-center justify-between gap-4">
        <nav className="flex items-center gap-5 text-sm">
          <Link to="/" className="font-heading text-lg font-semibold">
            Raptor
          </Link>
          <Link to="/" className={link} activeOptions={{ exact: true }}>
            Orgs
          </Link>
          <Link to="/settings/security" className={link}>
            Security
          </Link>
        </nav>
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
      {children}
    </div>
  );
}
