import {
  createContext,
  type FormEvent,
  type ReactNode,
  useCallback,
  useContext,
  useRef,
  useState,
} from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { BeginReauthResponse } from "@/gen/raptor/panel/v1/auth_pb";
import { message, needsReauth } from "@/lib/errors";
import { authClient } from "@/lib/transport";
import { getPasskey, passkeyCancelled } from "@/lib/webauthn";

// Sensitive account changes need the user to have confirmed it's them in
// the last 5 minutes. withReauth runs a change and, if the Panel asks,
// confirms (passkey, authenticator app, or emailed code) and runs it again.
type Reauth = <T>(change: () => Promise<T>) => Promise<T>;

const ReauthContext = createContext<Reauth | null>(null);

export function useReauth(): Reauth {
  const r = useContext(ReauthContext);
  if (!r) throw new Error("useReauth needs a ReauthProvider");
  return r;
}

type Pending = { resolve: () => void; reject: (err: unknown) => void };

export function ReauthProvider({ children }: { children: ReactNode }) {
  const [begin, setBegin] = useState<BeginReauthResponse | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<Pending | null>(null);

  const confirm = useCallback(async () => {
    const b = await authClient.beginReauth({});
    setBegin(b);
    setCode("");
    setError("");
    await new Promise<void>((resolve, reject) => {
      pending.current = { resolve, reject };
    });
  }, []);

  const withReauth: Reauth = useCallback(
    async (change) => {
      try {
        return await change();
      } catch (err) {
        if (!needsReauth(err)) throw err;
      }
      await confirm();
      return change();
    },
    [confirm],
  );

  function close(err?: unknown) {
    setBegin(null);
    if (err) pending.current?.reject(err);
    else pending.current?.resolve();
    pending.current = null;
  }

  async function withPasskey() {
    const ch = begin?.method.case === "passkey" ? begin.method.value : undefined;
    if (!ch) return;
    setBusy(true);
    setError("");
    try {
      const credentialJson = await getPasskey(ch.optionsJson);
      await authClient.finishReauth({
        proof: { case: "passkey", value: { ceremonyId: ch.ceremonyId, credentialJson } },
      });
      close();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function withCode(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      const proof =
        begin?.method.case === "emailSent"
          ? ({ case: "emailCode", value: code } as const)
          : ({ case: "totpCode", value: code } as const);
      await authClient.finishReauth({ proof });
      close();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  const hasPasskey = begin?.method.case === "passkey";
  const emailed = begin?.method.case === "emailSent";
  const codeLabel = emailed ? "The code we emailed you" : "The code from your authenticator app";

  return (
    <ReauthContext.Provider value={withReauth}>
      {children}
      <Dialog
        open={begin !== null}
        onOpenChange={(open) => !open && close(new Error("Cancelled."))}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Confirm it's you</DialogTitle>
            <DialogDescription>
              This changes how your account is secured, so it needs a fresh confirmation.
            </DialogDescription>
          </DialogHeader>
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          {hasPasskey && (
            <Button onClick={withPasskey} disabled={busy}>
              Confirm with a passkey
            </Button>
          )}
          {(emailed || begin?.totpAllowed) && (
            <form onSubmit={withCode} className="flex flex-col gap-2">
              <Label htmlFor="reauth-code">{codeLabel}</Label>
              <Input
                id="reauth-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                required
                value={code}
                onChange={(e) => setCode(e.target.value)}
              />
              <Button type="submit" variant={hasPasskey ? "outline" : "default"} disabled={busy}>
                Confirm
              </Button>
            </form>
          )}
        </DialogContent>
      </Dialog>
    </ReauthContext.Provider>
  );
}
