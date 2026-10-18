import { useMutation } from "@connectrpc/connect-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { Loader2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { AuthLayout } from "@/components/auth-layout";

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

  if (error) {
    return (
      <AuthLayout title="That link didn't work">
        <Alert variant="destructive" role="alert">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
        <Link to="/signin" className={buttonVariants({ size: "lg" })}>
          Back to sign in
        </Link>
      </AuthLayout>
    );
  }
  return (
    <AuthLayout title="Signing you in">
      <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Checking the link from your email…
      </p>
    </AuthLayout>
  );
}
