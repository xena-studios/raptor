import { useQuery } from "@connectrpc/connect-query";
import { Link } from "@tanstack/react-router";
import { CheckCircle2, Copy, Loader2 } from "lucide-react";
import { useState } from "react";

import { useReauth } from "@/components/reauth";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { OrgService } from "@/gen/raptor/panel/v1/org_pb";
import { message } from "@/lib/errors";
import { signPin } from "@/lib/signed";
import { orgClient } from "@/lib/transport";
import { passkeyCancelled } from "@/lib/webauthn";

// ConnectNode links a new machine: a join token, optionally the owner's
// passkey pinned to it, the install command, and then the node itself
// arriving (the page watches for it).
export function ConnectNode({ orgId, userId }: { orgId: string; userId: string }) {
  const passkeys = useQuery(AuthService.method.listPasskeys, {});
  const withReauth = useReauth();
  const [token, setToken] = useState<string | null>(null);
  const [known, setKnown] = useState<Set<string>>(new Set());
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  // The passkey the new node will trust: its fingerprint once signed, or
  // "skipped".
  const [pinned, setPinned] = useState<{ fingerprint: string; name: string } | "skipped" | null>(
    null,
  );
  const nodes = useQuery(
    OrgService.method.listNodes,
    { orgId },
    { refetchInterval: token ? 3_000 : false },
  );
  const arrived = token ? nodes.data?.nodes.find((n) => !known.has(n.id)) : undefined;

  async function start() {
    setError("");
    try {
      const res = await withReauth(() => orgClient.createJoinToken({ orgId }));
      setKnown(new Set((nodes.data?.nodes ?? []).map((n) => n.id)));
      setToken(res.token);
      setPinned((passkeys.data?.passkeys.length ?? 0) > 0 ? null : "skipped");
    } catch (err) {
      setError(message(err));
    }
  }

  async function trustPasskey() {
    if (!token) return;
    setError("");
    try {
      const pin = await signPin({
        joinToken: token,
        userId,
        passkeys: passkeys.data?.passkeys ?? [],
      });
      await orgClient.pinJoinToken({ orgId, token, pinJson: pin.pinJson });
      setPinned({ fingerprint: pin.fingerprint, name: pin.name });
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    }
  }

  const command = token
    ? `curl -fsSL https://get.raptorpanel.net | sudo bash -s -- -token ${token}`
    : "";

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <CardTitle>1. Make a join token</CardTitle>
          <CardDescription>
            It works once, for an hour, on a fresh Debian 12, Debian 13, or Ubuntu 24.04 server.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {token ? (
            <p className="text-sm text-muted-foreground">Token made.</p>
          ) : (
            <Button onClick={start}>Make a join token</Button>
          )}
        </CardContent>
      </Card>

      {token && (
        <Card>
          <CardHeader>
            <CardTitle>2. Trust your passkey</CardTitle>
            <CardDescription>
              Deleting servers and other dangerous actions need a passkey the node trusts. Signing
              now makes the node trust yours from the start. Your password manager asks twice: once
              to pick the passkey, once to sign.
            </CardDescription>
          </CardHeader>
          <CardContent>
            {pinned === null ? (
              <div className="flex gap-2">
                <Button size="sm" onClick={trustPasskey}>
                  Sign with a passkey
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setPinned("skipped")}>
                  Skip
                </Button>
              </div>
            ) : pinned === "skipped" ? (
              <p className="text-sm text-muted-foreground">
                Skipped. Pair one later with <code>sudo raptor keys reset</code> on the node.
              </p>
            ) : (
              <p className="text-sm">
                It will trust "{pinned.name}". When it links, it prints a fingerprint: check it's{" "}
                <code className="font-semibold">{pinned.fingerprint}</code>.
              </p>
            )}
          </CardContent>
        </Card>
      )}

      {token && pinned !== null && (
        <Card>
          <CardHeader>
            <CardTitle>3. Run this on the server, as root</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-start gap-2">
              <code className="flex-1 break-all rounded-md bg-muted p-3 font-mono text-xs">
                {command}
              </code>
              <Button
                size="icon-sm"
                variant="outline"
                aria-label="Copy the command"
                onClick={async () => {
                  await navigator.clipboard.writeText(command);
                  setCopied(true);
                }}
              >
                {copied ? <CheckCircle2 /> : <Copy />}
              </Button>
            </div>
            {arrived ? (
              <p className="flex items-center gap-2 text-sm">
                <CheckCircle2 className="size-4 text-emerald-500" />
                <span>
                  <span className="font-medium">{arrived.name}</span> linked
                  {arrived.connected ? " and connected" : ""}.{" "}
                  <Link
                    to="/orgs/$orgId/nodes/$nodeId"
                    params={{ orgId, nodeId: arrived.id }}
                    className="underline"
                  >
                    Open it
                  </Link>
                </span>
              </p>
            ) : (
              <p className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" /> Waiting for the node to link…
              </p>
            )}
          </CardContent>
        </Card>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
    </div>
  );
}
