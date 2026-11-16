import { useQuery } from "@connectrpc/connect-query";
import { createFileRoute } from "@tanstack/react-router";
import { Check, Loader2, Smartphone, X } from "lucide-react";
import { useState } from "react";

import { AuthLayout } from "@/components/auth-layout";
import { useReauth } from "@/components/reauth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { message } from "@/lib/errors";
import { requireSession } from "@/lib/session";
import { authClient } from "@/lib/transport";

// /authorize: Raptor's phone app opens this in the system browser to sign
// in (docs/PANEL.md#the-phone-app). The signed-in user approves it, and the
// browser goes back to the app with a one-time code, which only the app
// that started this can use (PKCE).

// The apps that may ask, and the only places the code may be sent. The
// Panel checks the same list (auth.AppClients); this copy is so a denial
// never sends the browser somewhere it wasn't registered.
const clients: Record<string, { name: string; redirects: string[] }> = {
  "raptor-mobile": { name: "Raptor's app", redirects: ["net.raptorpanel.app:/oauth"] },
};

type Search = {
  client_id?: string;
  redirect_uri?: string;
  code_challenge?: string;
  code_challenge_method?: string;
  state?: string;
  device?: string;
};

const str = (v: unknown) => (typeof v === "string" ? v : undefined);

export const Route = createFileRoute("/authorize")({
  validateSearch: (s: Record<string, unknown>): Search => ({
    client_id: str(s.client_id),
    redirect_uri: str(s.redirect_uri),
    code_challenge: str(s.code_challenge),
    code_challenge_method: str(s.code_challenge_method),
    state: str(s.state),
    device: str(s.device),
  }),
  beforeLoad: ({ location }) => requireSession(location),
  component: Authorize,
});

const can = [
  "See your orgs, nodes, and servers",
  "Start, stop, and restart servers, and take backups",
  "Send your alerts to the phone",
];
const cannot = [
  "Delete or wipe anything, or change who has access",
  "Change your account's sign-in or security",
  "See billing",
];

function back(redirect: string, params: Record<string, string>) {
  const q = new URLSearchParams(params).toString();
  window.location.assign(`${redirect}${redirect.includes("?") ? "&" : "?"}${q}`);
}

function Authorize() {
  const search = Route.useSearch();
  const session = useQuery(AuthService.method.getSession, {});
  const withReauth = useReauth();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState<"approved" | "denied" | null>(null);

  const client = clients[search.client_id ?? ""];
  const redirect = search.redirect_uri ?? "";
  const state = search.state ?? "";
  const device = (search.device ?? "").trim().slice(0, 64) || "your phone";
  const valid =
    client?.redirects.includes(redirect) &&
    search.code_challenge_method === "S256" &&
    !!search.code_challenge &&
    !!state;

  if (!client || !valid) {
    return (
      <AuthLayout title="This sign-in link isn't right">
        <Alert variant="destructive">
          <AlertDescription>
            It's missing something the app should have sent. Go back to Raptor's app and tap Sign in
            again.
          </AlertDescription>
        </Alert>
      </AuthLayout>
    );
  }

  async function approve() {
    setBusy(true);
    setError("");
    try {
      const res = await withReauth(() =>
        authClient.approveApp({
          clientId: search.client_id,
          redirectUri: redirect,
          codeChallenge: search.code_challenge,
          deviceName: device,
        }),
      );
      setDone("approved");
      back(redirect, { code: res.code, state });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  function deny() {
    setDone("denied");
    back(redirect, { error: "access_denied", state });
  }

  if (done) {
    return (
      <AuthLayout
        title={done === "approved" ? "You're signed in" : "Sign-in cancelled"}
        description="You can go back to the app now. If it didn't open by itself, switch to it."
      >
        {null}
      </AuthLayout>
    );
  }

  const email = session.data?.user?.email;
  return (
    <AuthLayout
      title={`Sign in to ${client.name} on ${device}?`}
      description={
        email ? (
          <>
            As <span className="font-medium text-foreground">{email}</span>. You'll confirm it's you
            first.
          </>
        ) : undefined
      }
    >
      <div className="flex flex-col gap-4 text-sm">
        <div className="flex items-center gap-3 border-b pb-4">
          <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border text-muted-foreground">
            <Smartphone className="size-4" />
          </span>
          <p className="text-muted-foreground">
            Only continue if you just tapped Sign in in the app. It stays signed in until you sign
            it out, here under Devices or in the app, or don't use it for 30 days.
          </p>
        </div>
        <ul className="flex flex-col gap-2">
          {can.map((c) => (
            <li key={c} className="flex gap-2">
              <Check className="mt-0.5 size-4 shrink-0 text-primary" /> {c}
            </li>
          ))}
        </ul>
        <div className="flex flex-col gap-2">
          <p className="font-mono text-xs tracking-wide text-muted-foreground uppercase">
            Only in the web app
          </p>
          <ul className="flex flex-col gap-2 text-muted-foreground">
            {cannot.map((c) => (
              <li key={c} className="flex gap-2">
                <X className="mt-0.5 size-4 shrink-0" /> {c}
              </li>
            ))}
          </ul>
        </div>
      </div>
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="flex flex-col gap-2">
        <Button onClick={approve} disabled={busy}>
          {busy && <Loader2 className="animate-spin" />} Sign in
        </Button>
        <Button variant="ghost" onClick={deny} disabled={busy}>
          Cancel
        </Button>
      </div>
    </AuthLayout>
  );
}
