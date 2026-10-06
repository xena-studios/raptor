import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { message } from "@/lib/errors";
import { currentSession } from "@/lib/session";
import { orgClient } from "@/lib/transport";

// /invite#<token>: an emailed invitation. The token is after #, so it never
// reaches a server; it's kept in this tab's storage while the user signs in.
export const Route = createFileRoute("/invite")({ component: Invite });

const key = "raptor.invite";

function Invite() {
  const navigate = useNavigate();
  const [error, setError] = useState("");
  const tried = useRef(false);

  useEffect(() => {
    if (tried.current) return;
    tried.current = true;
    const fromHash = window.location.hash.slice(1);
    if (fromHash) sessionStorage.setItem(key, fromHash);
    window.history.replaceState(null, "", window.location.pathname);
    const token = sessionStorage.getItem(key);
    if (!token) {
      setError("This invitation link is missing its code. Open it from the email again.");
      return;
    }
    (async () => {
      if (!(await currentSession())) {
        await navigate({ to: "/signin", search: { next: "/invite" } });
        return;
      }
      try {
        const res = await orgClient.acceptInvitation({ token });
        sessionStorage.removeItem(key);
        await navigate({ to: "/orgs/$orgId", params: { orgId: res.org?.id ?? "" }, replace: true });
      } catch (err) {
        sessionStorage.removeItem(key);
        setError(message(err));
      }
    })();
  }, [navigate]);

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col justify-center gap-4 p-6">
      {error ? (
        <>
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
          <Link to="/" className={buttonVariants()}>
            Go to your orgs
          </Link>
        </>
      ) : (
        <p className="text-sm text-muted-foreground">Accepting the invitation…</p>
      )}
    </main>
  );
}
