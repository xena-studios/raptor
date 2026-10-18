import { useMutation, useQuery } from "@connectrpc/connect-query";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { cn } from "cn";
import { ArrowLeft, KeyRound, Loader2, Mail } from "lucide-react";
import { type FormEvent, useCallback, useRef, useState } from "react";

import { AuthLayout } from "@/components/auth-layout";
import { ProviderIcon } from "@/components/provider-icons";
import { Turnstile, type TurnstileHandle, turnstileOn } from "@/components/turnstile";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { message } from "@/lib/errors";
import { currentSession, safeNext } from "@/lib/session";
import { getPasskey, passkeyCancelled, passkeysSupported } from "@/lib/webauthn";

type Search = { next?: string; error?: string };

export const Route = createFileRoute("/signin/")({
  validateSearch: (s: Record<string, unknown>): Search => ({
    next: typeof s.next === "string" ? s.next : undefined,
    error: typeof s.error === "string" ? s.error : undefined,
  }),
  beforeLoad: async ({ search }) => {
    if (await currentSession()) throw redirect({ href: safeNext(search.next) });
  },
  component: SignIn,
});

// What the API's OAuth callback means by its error codes.
const callbackErrors: Record<string, string> = {
  oauth_cancelled: "Signing in was cancelled.",
  oauth_failed: "Signing in with that account didn't work. Try again.",
  oauth_unverified:
    "That account's email address isn't verified with the provider. Verify it there first, or sign in by email.",
  rate_limited: "Too many attempts. Wait a while and try again.",
};

const providerNames: Record<string, string> = {
  google: "Google",
  github: "GitHub",
  discord: "Discord",
};

function SignIn() {
  const { next, error: callbackError } = Route.useSearch();
  const navigate = useNavigate();
  const methods = useQuery(AuthService.method.getSignInMethods, {});
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [sent, setSent] = useState(false);
  const [turnstileToken, setTurnstileToken] = useState("");
  const turnstile = useRef<TurnstileHandle>(null);
  const onToken = useCallback((t: string) => setTurnstileToken(t), []);
  const [error, setError] = useState(
    callbackError ? (callbackErrors[callbackError] ?? callbackErrors.oauth_failed) : "",
  );

  const start = useMutation(AuthService.method.startEmailSignIn);
  const finish = useMutation(AuthService.method.finishEmailSignIn);
  const beginPasskey = useMutation(AuthService.method.beginPasskeySignIn);
  const finishPasskey = useMutation(AuthService.method.finishPasskeySignIn);
  const beginOAuth = useMutation(AuthService.method.beginOAuth);

  const done = (secondFactor: boolean) =>
    secondFactor
      ? navigate({ to: "/signin/second-factor", search: { next } })
      : navigate({ href: safeNext(next) });

  async function sendCode(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await start.mutateAsync({ email, turnstileToken });
      setSent(true);
    } catch (err) {
      setError(message(err));
    } finally {
      // A token works once.
      turnstile.current?.reset();
    }
  }

  async function checkCode(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      const res = await finish.mutateAsync({ proof: { case: "code", value: { email, code } } });
      await done(res.secondFactorRequired);
    } catch (err) {
      setError(message(err));
    }
  }

  async function withPasskey() {
    setError("");
    try {
      const begin = await beginPasskey.mutateAsync({});
      if (!begin.challenge) throw new Error("The Panel didn't send a challenge.");
      const credentialJson = await getPasskey(begin.challenge.optionsJson);
      await finishPasskey.mutateAsync({
        answer: { ceremonyId: begin.challenge.ceremonyId, credentialJson },
      });
      await done(false);
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    }
  }

  async function withProvider(provider: string) {
    setError("");
    try {
      const res = await beginOAuth.mutateAsync({ provider, link: false });
      window.location.assign(res.url);
    } catch (err) {
      setError(message(err));
    }
  }

  const m = methods.data;
  const others = !!m && (m.passkeys || m.oauthProviders.length > 0);

  if (sent) {
    return (
      <AuthLayout
        title="Check your email"
        description={
          <>
            We sent a 6-digit code to <span className="font-medium text-foreground">{email}</span>.
            Type it here, or open the link in the email. Either works once, for 10 minutes.
          </>
        }
      >
        <form onSubmit={checkCode} className="flex flex-col gap-4">
          {error && <SignInError text={error} />}
          <div className="flex flex-col gap-2">
            <Label htmlFor="code">Code</Label>
            <Input
              id="code"
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9 ]*"
              maxLength={7}
              required
              autoFocus
              placeholder="000000"
              className="h-11 text-center font-mono text-lg tracking-[0.4em] placeholder:tracking-[0.4em]"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </div>
          <Button type="submit" size="lg" disabled={finish.isPending}>
            {finish.isPending && <Loader2 className="animate-spin" />} Sign in
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              setSent(false);
              setCode("");
              setError("");
            }}
          >
            <ArrowLeft /> Use a different email
          </Button>
        </form>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout
      title="Sign in to Raptor"
      description="No password to remember. New here? Signing in makes your account."
    >
      <div className="flex flex-col gap-4">
        {error && <SignInError text={error} />}

        {methods.isPending && (
          <div
            className="flex flex-col gap-2"
            aria-busy="true"
            role="status"
            aria-label="Loading sign-in options"
          >
            <Skeleton className="h-9" />
            <Skeleton className="h-9" />
            <Skeleton className="mt-4 h-9" />
          </div>
        )}

        {m?.passkeys && passkeysSupported() && (
          <Button
            size="lg"
            onClick={withPasskey}
            disabled={beginPasskey.isPending || finishPasskey.isPending}
          >
            {beginPasskey.isPending || finishPasskey.isPending ? (
              <Loader2 className="animate-spin" />
            ) : (
              <KeyRound />
            )}{" "}
            Sign in with a passkey
          </Button>
        )}

        {m && m.oauthProviders.length > 0 && (
          <div className="flex flex-col gap-2">
            {m.oauthProviders.map((p) => (
              <Button
                key={p}
                size="lg"
                variant="outline"
                onClick={() => withProvider(p)}
                disabled={beginOAuth.isPending}
              >
                <ProviderIcon provider={p} /> Continue with {providerNames[p] ?? p}
              </Button>
            ))}
          </div>
        )}

        {m?.email && (
          <>
            {others && (
              <div className="flex items-center gap-3 py-1" aria-hidden="true">
                <Separator className="flex-1" />
                <span className="font-mono text-[0.7rem] tracking-wide text-muted-foreground uppercase">
                  or by email
                </span>
                <Separator className="flex-1" />
              </div>
            )}
            <form onSubmit={sendCode} className="flex flex-col gap-3">
              <div className="flex flex-col gap-2">
                <Label htmlFor="email" className={cn(others && "sr-only")}>
                  Email
                </Label>
                <Input
                  id="email"
                  type="email"
                  autoComplete="email webauthn"
                  placeholder="you@example.com"
                  required
                  className="h-9"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </div>
              <Turnstile ref={turnstile} onToken={onToken} />
              <Button
                type="submit"
                size="lg"
                variant={others ? "secondary" : "default"}
                disabled={start.isPending || (turnstileOn && !turnstileToken)}
              >
                {start.isPending ? <Loader2 className="animate-spin" /> : <Mail />} Email me a code
              </Button>
            </form>
          </>
        )}

        {methods.error && <SignInError text={message(methods.error)} />}
        {m && !m.email && !m.passkeys && m.oauthProviders.length === 0 && (
          <p className="text-sm text-muted-foreground">
            Signing in isn't set up on this Panel yet.
          </p>
        )}
      </div>
    </AuthLayout>
  );
}

function SignInError({ text }: { text: string }) {
  return (
    <Alert variant="destructive" role="alert">
      <AlertDescription>{text}</AlertDescription>
    </Alert>
  );
}
