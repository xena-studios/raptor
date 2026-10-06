import { useMutation, useQuery } from "@connectrpc/connect-query";
import { createFileRoute, redirect, useNavigate } from "@tanstack/react-router";
import { KeyRound, Mail } from "lucide-react";
import { type FormEvent, useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
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
      await start.mutateAsync({ email });
      setSent(true);
    } catch (err) {
      setError(message(err));
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
  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col justify-center p-6">
      <Card>
        <CardHeader>
          <CardTitle className="text-xl">Sign in to Raptor</CardTitle>
          <CardDescription>
            No password: a passkey, an emailed code, or an account you already have.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}

          {m?.passkeys && passkeysSupported() && (
            <Button
              size="lg"
              onClick={withPasskey}
              disabled={beginPasskey.isPending || finishPasskey.isPending}
            >
              <KeyRound /> Sign in with a passkey
            </Button>
          )}

          {m && m.oauthProviders.length > 0 && (
            <div className="flex flex-col gap-2">
              {m.oauthProviders.map((p) => (
                <Button
                  key={p}
                  variant="outline"
                  onClick={() => withProvider(p)}
                  disabled={beginOAuth.isPending}
                >
                  Continue with {providerNames[p] ?? p}
                </Button>
              ))}
            </div>
          )}

          {m?.email && (
            <>
              {(m.passkeys || m.oauthProviders.length > 0) && <Separator />}
              {!sent ? (
                <form onSubmit={sendCode} className="flex flex-col gap-2">
                  <Label htmlFor="email">Email</Label>
                  <Input
                    id="email"
                    type="email"
                    autoComplete="email webauthn"
                    required
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                  />
                  <Button type="submit" variant="secondary" disabled={start.isPending}>
                    <Mail /> Email me a code
                  </Button>
                </form>
              ) : (
                <form onSubmit={checkCode} className="flex flex-col gap-2">
                  <Label htmlFor="code">The 6-digit code we emailed to {email}</Label>
                  <Input
                    id="code"
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    pattern="[0-9 ]*"
                    maxLength={7}
                    required
                    autoFocus
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                  />
                  <p className="text-xs text-muted-foreground">
                    Or open the link in the email. Either works once, for 10 minutes.
                  </p>
                  <Button type="submit" disabled={finish.isPending}>
                    Sign in
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    onClick={() => {
                      setSent(false);
                      setCode("");
                    }}
                  >
                    Use a different email
                  </Button>
                </form>
              )}
            </>
          )}

          {methods.error && <p className="text-sm text-destructive">{message(methods.error)}</p>}
          {m && !m.email && !m.passkeys && m.oauthProviders.length === 0 && (
            <p className="text-sm text-muted-foreground">
              Signing in isn't set up on this Panel yet.
            </p>
          )}
        </CardContent>
      </Card>
    </main>
  );
}
