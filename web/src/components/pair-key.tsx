import { useQuery } from "@connectrpc/connect-query";
import { KeyRound } from "lucide-react";
import { type FormEvent, useState } from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AuthService, type Passkey } from "@/gen/raptor/panel/v1/auth_pb";
import { keyFingerprint } from "@/lib/canonical";
import { message } from "@/lib/errors";
import { sameBytes, sendSigned, whichPasskey } from "@/lib/signed";
import { passkeyCancelled } from "@/lib/webauthn";

const b64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));

// PairKey trusts one of the user's passkeys on this node after root ran
// `raptor keys reset` there: the passkey signs keys.pair with the code
// from the box, and root confirms the fingerprint shown here on the box.
export function PairKey({ nodeId, userId }: { nodeId: string; userId: string }) {
  const passkeys = useQuery(AuthService.method.listPasskeys, {});
  const [code, setCode] = useState("");
  const [fingerprint, setFingerprint] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const list = passkeys.data?.passkeys ?? [];

  async function pair(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      // First the password manager says which passkey (no list of allowed
      // ones: 1Password and others step aside when given one), then that
      // passkey signs the pairing, which names it.
      const id = await whichPasskey();
      const key: Passkey | undefined = list.find((p) => sameBytes(p.credentialId, id));
      if (!key)
        throw new Error("That passkey isn't on your Raptor account. Add it under Security first.");
      // The fingerprint comes from the key this page signs with, so a
      // Panel that swapped in its own key can't make them match.
      const mine = await keyFingerprint(key.publicKey);
      const res = await sendSigned({
        userId,
        nodeId,
        action: "keys.pair",
        params: {
          code,
          credential_id: b64(key.credentialId),
          public_key: b64(key.publicKey),
          user_id: userId,
          name: key.name,
        },
        expect: key.credentialId,
      });
      const theirs = (JSON.parse(res.resultJson || "{}") as { fingerprint?: string }).fingerprint;
      if (theirs && theirs !== mine)
        throw new Error("The node reports a different key. Don't confirm it on the node.");
      setFingerprint(mine);
      setCode("");
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <KeyRound className="size-4" /> Trust a passkey on this node
        </CardTitle>
        <CardDescription>
          Deleting servers and other dangerous actions must be signed by a passkey the node trusts.
          On the node, run <code>sudo raptor keys reset</code>, then enter the code it shows. Your
          password manager asks twice: once to pick the passkey, once to sign.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {fingerprint ? (
          <Alert>
            <AlertDescription>
              Now confirm on the node that it shows this fingerprint, and only then: <br />
              <code className="text-base font-semibold">{fingerprint}</code>
            </AlertDescription>
          </Alert>
        ) : list.length === 0 ? (
          <p className="text-sm text-muted-foreground">Add a passkey in Security first.</p>
        ) : (
          <form onSubmit={pair} className="flex flex-col gap-2">
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Label htmlFor="pair-code">Pairing code</Label>
            <Input
              id="pair-code"
              required
              autoComplete="off"
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
            <Button type="submit" className="self-start" disabled={busy}>
              Sign with a passkey
            </Button>
          </form>
        )}
      </CardContent>
    </Card>
  );
}
