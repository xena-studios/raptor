import { useMutation } from "@connectrpc/connect-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { type FormEvent, useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { message } from "@/lib/errors";
import { safeNext } from "@/lib/session";

// The second step after an email or provider sign-in, for accounts with an
// authenticator app. The pending sign-in is in a cookie the API set.
export const Route = createFileRoute("/signin/second-factor")({
  validateSearch: (s: Record<string, unknown>): { next?: string } => ({
    next: typeof s.next === "string" ? s.next : undefined,
  }),
  component: SecondFactor,
});

function SecondFactor() {
  const { next } = Route.useSearch();
  const navigate = useNavigate();
  const finish = useMutation(AuthService.method.finishSecondFactor);
  const [recovery, setRecovery] = useState(false);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [left, setLeft] = useState<number | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      const res = await finish.mutateAsync({
        proof: recovery ? { case: "recoveryCode", value: code } : { case: "totpCode", value: code },
      });
      if (recovery) {
        // Say how many are left before moving on.
        setLeft(res.recoveryCodesLeft);
        return;
      }
      await navigate({ href: safeNext(next), replace: true });
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col justify-center p-6">
      <Card>
        <CardHeader>
          <CardTitle className="text-xl">Two-factor authentication</CardTitle>
          <CardDescription>
            {recovery
              ? "Enter one of your recovery codes. Each works once."
              : "Enter the 6-digit code from your authenticator app."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {left !== null ? (
            <div className="flex flex-col gap-3">
              <Alert>
                <AlertDescription>
                  You're signed in.{" "}
                  {left === 0
                    ? "That was your last recovery code"
                    : `You have ${left} recovery codes left`}
                  : make new ones in your account's security settings, and set your authenticator
                  app up again if you've lost it.
                </AlertDescription>
              </Alert>
              <Button onClick={() => navigate({ href: safeNext(next), replace: true })}>
                Continue
              </Button>
            </div>
          ) : (
            <form onSubmit={submit} className="flex flex-col gap-3">
              {error && (
                <Alert variant="destructive">
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              )}
              <Label htmlFor="code">{recovery ? "Recovery code" : "Code"}</Label>
              <Input
                id="code"
                autoFocus
                required
                autoComplete={recovery ? "off" : "one-time-code"}
                inputMode={recovery ? "text" : "numeric"}
                value={code}
                onChange={(e) => setCode(e.target.value)}
              />
              <Button type="submit" disabled={finish.isPending}>
                Continue
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  setRecovery(!recovery);
                  setCode("");
                  setError("");
                }}
              >
                {recovery ? "Use the authenticator app" : "Use a recovery code"}
              </Button>
              <Link to="/signin" className={buttonVariants({ variant: "link" })}>
                Start over
              </Link>
            </form>
          )}
        </CardContent>
      </Card>
    </main>
  );
}
