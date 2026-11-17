import { useMutation, useQuery } from "@connectrpc/connect-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { Loader2, Play } from "lucide-react";
import { useCallback, useRef, useState } from "react";

import { AuthLayout } from "@/components/auth-layout";
import { Turnstile, type TurnstileHandle, turnstileOn } from "@/components/turnstile";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { demoState, getRaptorURL, isGuest } from "@/lib/demo";
import { message } from "@/lib/errors";
import { currentSession } from "@/lib/session";

// /demo: the public demo's door (#449), linked from raptorpanel.net's "Try
// the demo". A security check, then a guest session in the demo org for
// an hour: no email, no passkey. When no demo org is set up, it says the
// demo isn't open.
export const Route = createFileRoute("/demo")({
  beforeLoad: async () => {
    const s = await currentSession();
    return {
      signedIn: !s
        ? ("none" as const)
        : isGuest(s.user)
          ? ("guest" as const)
          : ("account" as const),
    };
  },
  component: Demo,
});

function Demo() {
  const { signedIn } = Route.useRouteContext();
  const navigate = useNavigate();
  const demo = useQuery(AuthService.method.getDemo, {});
  const start = useMutation(AuthService.method.startDemo);
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const turnstile = useRef<TurnstileHandle>(null);
  const onToken = useCallback((t: string) => setToken(t), []);

  const state = demoState({
    open: demo.error ? false : demo.data?.open,
    signedIn,
    starting: start.isPending,
    error,
  });

  async function begin() {
    setError("");
    try {
      const res = await start.mutateAsync({ turnstileToken: token });
      await navigate({ to: "/orgs/$orgId", params: { orgId: res.orgId } });
    } catch (err) {
      setError(message(err));
      // A token works once.
      turnstile.current?.reset();
    }
  }

  if (state.kind === "signed-in") {
    return (
      <AuthLayout
        title="You're signed in"
        description="The demo signs you in as a guest, so it would sign you out of your account. Your own Raptor has everything the demo does."
      >
        <Link to="/" className={buttonVariants({ size: "lg" })}>
          Go to your dashboard
        </Link>
      </AuthLayout>
    );
  }
  if (state.kind === "closed") {
    return (
      <AuthLayout
        title="The demo isn't open right now"
        description="Check back soon. In the meantime, Raptor is free to try on your own machine."
      >
        <a href={getRaptorURL} className={buttonVariants({ size: "lg" })}>
          Get Raptor
        </a>
      </AuthLayout>
    );
  }
  return (
    <AuthLayout
      title="Try Raptor"
      description="A real Panel with real game servers: watch the console, edit files, install add-ons, and take backups. No account needed. You get an hour, and the demo resets every hour."
    >
      <div className="flex flex-col gap-3">
        {state.kind === "checking" ? (
          <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> Checking the demo is open…
          </p>
        ) : (
          <>
            <Turnstile ref={turnstile} onToken={onToken} />
            <Button
              size="lg"
              onClick={begin}
              disabled={state.kind === "starting" || (turnstileOn && !token)}
            >
              {state.kind === "starting" ? <Loader2 className="animate-spin" /> : <Play />} Start
              the demo
            </Button>
          </>
        )}
        {state.kind === "failed" && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{state.message}</AlertDescription>
          </Alert>
        )}
        <p className="text-xs text-muted-foreground">
          Other visitors share the demo's servers, so don't put anything private in them.
        </p>
      </div>
    </AuthLayout>
  );
}
