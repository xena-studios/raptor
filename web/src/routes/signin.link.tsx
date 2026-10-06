import { useMutation } from "@connectrpc/connect-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { buttonVariants } from "@/components/ui/button";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { message } from "@/lib/errors";

// /signin/link#<token>: the emailed sign-in link. The token is after #, so
// it never reaches a server; this page reads it and sends it to the API.
export const Route = createFileRoute("/signin/link")({ component: SignInLink });

function SignInLink() {
  const navigate = useNavigate();
  const finish = useMutation(AuthService.method.finishEmailSignIn);
  const [error, setError] = useState("");
  const tried = useRef(false);

  useEffect(() => {
    if (tried.current) return;
    tried.current = true;
    const token = window.location.hash.slice(1);
    // Out of the address bar and history: it's used up either way.
    window.history.replaceState(null, "", window.location.pathname);
    if (!token) {
      setError("This link is missing its code. Open the link from the email again.");
      return;
    }
    finish
      .mutateAsync({ proof: { case: "linkToken", value: token } })
      .then((res) =>
        navigate({ to: res.secondFactorRequired ? "/signin/second-factor" : "/", replace: true }),
      )
      .catch((err) => setError(message(err)));
  }, [finish, navigate]);

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col justify-center gap-4 p-6">
      {error ? (
        <>
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
          <Link to="/signin" className={buttonVariants()}>
            Back to sign in
          </Link>
        </>
      ) : (
        <p className="text-sm text-muted-foreground">Signing you in…</p>
      )}
    </main>
  );
}
