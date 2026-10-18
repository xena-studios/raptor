import { useMutation } from "@connectrpc/connect-query";
import { createFileRoute, Link, useNavigate } from "@tanstack/react-router";
import { cn } from "cn";
import { Loader2 } from "lucide-react";
import { type FormEvent, useState } from "react";

import { AuthLayout } from "@/components/auth-layout";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
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

  if (left !== null) {
    return (
      <AuthLayout title="You're signed in">
        <Alert>
          <AlertDescription>
            {left === 0
              ? "That was your last recovery code"
              : `You have ${left} recovery ${left === 1 ? "code" : "codes"} left`}
            : make new ones in your account's security settings, and set your authenticator app up
            again if you've lost it.
          </AlertDescription>
        </Alert>
        <Button size="lg" onClick={() => navigate({ href: safeNext(next), replace: true })}>
          Continue
        </Button>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout
      title="Two-factor authentication"
      description={
        recovery
          ? "Enter one of your recovery codes. Each works once."
          : "Enter the 6-digit code from your authenticator app."
      }
    >
      <form onSubmit={submit} className="flex flex-col gap-4">
        {error && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="flex flex-col gap-2">
          <Label htmlFor="code">{recovery ? "Recovery code" : "Code"}</Label>
          <Input
            id="code"
            autoFocus
            required
            autoComplete={recovery ? "off" : "one-time-code"}
            inputMode={recovery ? "text" : "numeric"}
            placeholder={recovery ? undefined : "000000"}
            className={cn(
              "h-11 font-mono",
              !recovery && "text-center text-lg tracking-[0.4em] placeholder:tracking-[0.4em]",
            )}
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
        </div>
        <Button type="submit" size="lg" disabled={finish.isPending}>
          {finish.isPending && <Loader2 className="animate-spin" />} Continue
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
        <p className="text-center text-xs text-muted-foreground">
          Not you?{" "}
          <Link to="/signin" className="underline underline-offset-2 hover:text-foreground">
            Start over
          </Link>
        </p>
      </form>
    </AuthLayout>
  );
}
