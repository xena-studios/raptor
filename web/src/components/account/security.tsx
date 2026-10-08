import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, Smartphone, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { renderSVG } from "uqr";

import { useReauth } from "@/components/reauth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { keyFingerprint } from "@/lib/canonical";
import { message } from "@/lib/errors";
import { authClient } from "@/lib/transport";
import { createPasskey, passkeyCancelled, passkeysSupported } from "@/lib/webauthn";

export const providerNames: Record<string, string> = {
  google: "Google",
  github: "GitHub",
  discord: "Discord",
};

export const linkErrors: Record<string, string> = {
  oauth_taken: "That account already signs in to a different Raptor account.",
  oauth_failed: "Linking that account didn't work. Try again.",
  oauth_cancelled: "Linking was cancelled.",
};

export function when(ts: { seconds: bigint; nanos: number } | undefined): string {
  return ts ? timestampDate(ts as Parameters<typeof timestampDate>[0]).toLocaleString() : "never";
}

export function Section({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">{children}</CardContent>
    </Card>
  );
}

export function ErrorLine({ error }: { error: string }) {
  return error ? <p className="text-sm text-destructive">{error}</p> : null;
}

export function useRefresh() {
  const client = useQueryClient();
  return () => client.invalidateQueries();
}

export function Passkeys() {
  const list = useQuery(AuthService.method.listPasskeys, {});
  const withReauth = useReauth();
  const refresh = useRefresh();
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function add(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const begin = await withReauth(() => authClient.beginPasskeyRegistration({}));
      if (!begin.challenge) throw new Error("The Panel didn't send a challenge.");
      const credentialJson = await createPasskey(begin.challenge.optionsJson);
      await authClient.finishPasskeyRegistration({
        answer: { ceremonyId: begin.challenge.ceremonyId, credentialJson },
        name,
      });
      setName("");
      await refresh();
    } catch (err) {
      if (!passkeyCancelled(err)) setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string) {
    setError("");
    try {
      await withReauth(() => authClient.deletePasskey({ id }));
      await refresh();
    } catch (err) {
      setError(message(err));
    }
  }

  async function rename(id: string, current: string) {
    const next = window.prompt("Name this passkey", current);
    if (next === null || next.trim() === current) return;
    try {
      await authClient.renamePasskey({ id, name: next });
      await refresh();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <Section
      title="Passkeys"
      description="Sign in with your fingerprint, face, or device PIN, and confirm dangerous actions on your servers. A passkey is both factors at once."
    >
      <ErrorLine error={error} />
      {list.data?.passkeys.length === 0 && (
        <p className="text-sm text-muted-foreground">No passkeys yet.</p>
      )}
      {list.data?.passkeys.map((p) => (
        <div key={p.id} className="flex items-center justify-between gap-3 rounded-lg border p-3">
          <div className="flex items-center gap-3">
            <KeyRound className="size-4 text-muted-foreground" />
            <div>
              <button
                type="button"
                className="font-medium hover:underline"
                onClick={() => rename(p.id, p.name)}
              >
                {p.name}
              </button>
              <p className="text-xs text-muted-foreground">
                Added {when(p.createdAt)} · last used {when(p.lastUsedAt)} ·{" "}
                <Fingerprint publicKey={p.publicKey} />
              </p>
            </div>
            {p.synced && <Badge variant="secondary">Synced</Badge>}
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Remove ${p.name}`}
            onClick={() => remove(p.id)}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      {passkeysSupported() ? (
        <form onSubmit={add} className="flex gap-2">
          <Input
            placeholder="Name (e.g. MacBook)"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={64}
          />
          <Button type="submit" disabled={busy}>
            Add a passkey
          </Button>
        </form>
      ) : (
        <p className="text-sm text-muted-foreground">This browser can't use passkeys.</p>
      )}
    </Section>
  );
}

function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const text = `Raptor recovery codes. Each works once.\n\n${codes.join("\n")}\n`;
  return (
    <Alert>
      <AlertDescription className="flex flex-col gap-3">
        <p>
          Save these recovery codes somewhere safe. Each signs you in once if you lose your phone.
          They won't be shown again.
        </p>
        <pre className="grid grid-cols-2 gap-1 font-mono text-sm">
          {codes.map((c) => (
            <span key={c}>{c}</span>
          ))}
        </pre>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => navigator.clipboard.writeText(text)}>
            Copy
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              const a = document.createElement("a");
              a.href = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
              a.download = "raptor-recovery-codes.txt";
              a.click();
              URL.revokeObjectURL(a.href);
            }}
          >
            Download
          </Button>
          <Button size="sm" onClick={onDone}>
            I've saved them
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  );
}

export function Authenticator() {
  const session = useQuery(AuthService.method.getSession, {});
  const withReauth = useReauth();
  const refresh = useRefresh();
  const [setup, setSetup] = useState<{ secret: string; url: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const on = session.data?.user?.totpEnabled ?? false;

  async function act(fn: () => Promise<void>) {
    setError("");
    try {
      await fn();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <Section
      title="Authenticator app"
      description="After an email or Google/GitHub/Discord sign-in, also ask for a code from an app like 1Password, Google Authenticator, or Aegis."
    >
      <ErrorLine error={error} />
      {codes && <RecoveryCodes codes={codes} onDone={() => setCodes(null)} />}
      {on && !codes && (
        <>
          <p className="flex items-center gap-2 text-sm">
            <Smartphone className="size-4" /> On. {session.data?.recoveryCodesLeft} recovery codes
            left.
          </p>
          <div className="flex gap-2">
            <Button
              variant="outline"
              onClick={() =>
                act(async () => {
                  const res = await withReauth(() => authClient.regenerateRecoveryCodes({}));
                  setCodes(res.recoveryCodes);
                  await refresh();
                })
              }
            >
              New recovery codes
            </Button>
            <Button
              variant="destructive"
              onClick={() =>
                act(async () => {
                  await withReauth(() => authClient.disableTOTP({}));
                  await refresh();
                })
              }
            >
              Turn off
            </Button>
          </div>
        </>
      )}
      {!on && !setup && (
        <Button
          className="self-start"
          onClick={() =>
            act(async () => {
              const res = await withReauth(() => authClient.beginTOTPSetup({}));
              setSetup({ secret: res.secret, url: res.url });
            })
          }
        >
          Set up
        </Button>
      )}
      {!on && setup && (
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            act(async () => {
              const res = await withReauth(() => authClient.finishTOTPSetup({ code }));
              setSetup(null);
              setCode("");
              setCodes(res.recoveryCodes);
              await refresh();
            });
          }}
        >
          <p className="text-sm">Scan this with your authenticator app, or type the key in.</p>
          {/* uqr renders the QR code locally: the secret never leaves the page. */}
          <div
            className="w-44 rounded-lg bg-white p-2"
            // biome-ignore lint/security/noDangerouslySetInnerHtml: uqr's own SVG, from the Panel's otpauth URL
            dangerouslySetInnerHTML={{ __html: renderSVG(setup.url) }}
          />
          <code className="break-all text-xs">{setup.secret}</code>
          <Label htmlFor="totp-code">The 6-digit code it shows</Label>
          <Input
            id="totp-code"
            inputMode="numeric"
            autoComplete="one-time-code"
            required
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
          <div className="flex gap-2">
            <Button type="submit">Turn on</Button>
            <Button type="button" variant="ghost" onClick={() => setSetup(null)}>
              Cancel
            </Button>
          </div>
        </form>
      )}
    </Section>
  );
}

export function LinkedAccounts() {
  const methods = useQuery(AuthService.method.getSignInMethods, {});
  const list = useQuery(AuthService.method.listOAuthAccounts, {});
  const withReauth = useReauth();
  const refresh = useRefresh();
  const [error, setError] = useState("");
  const providers = methods.data?.oauthProviders ?? [];
  if (providers.length === 0 && (list.data?.accounts.length ?? 0) === 0) return null;

  return (
    <Section title="Connections" description="Accounts elsewhere that can sign in to this one.">
      <ErrorLine error={error} />
      {list.data?.accounts.map((a) => (
        <div key={a.id} className="flex items-center justify-between rounded-lg border p-3 text-sm">
          <span>
            {providerNames[a.provider] ?? a.provider}{" "}
            <span className="text-muted-foreground">{a.email}</span>
          </span>
          <Button
            variant="ghost"
            size="sm"
            onClick={async () => {
              setError("");
              try {
                await withReauth(() => authClient.unlinkOAuthAccount({ id: a.id }));
                await refresh();
              } catch (err) {
                setError(message(err));
              }
            }}
          >
            Unlink
          </Button>
        </div>
      ))}
      <div className="flex gap-2">
        {providers.map((p) => (
          <Button
            key={p}
            variant="outline"
            onClick={async () => {
              setError("");
              try {
                const res = await withReauth(() =>
                  authClient.beginOAuth({ provider: p, link: true }),
                );
                window.location.assign(res.url);
              } catch (err) {
                setError(message(err));
              }
            }}
          >
            Link {providerNames[p] ?? p}
          </Button>
        ))}
      </div>
    </Section>
  );
}

export function Devices() {
  const list = useQuery(AuthService.method.listSessions, {});
  const refresh = useRefresh();
  const [error, setError] = useState("");
  async function revoke(
    target: { case: "id"; value: string } | { case: "allOthers"; value: boolean },
  ) {
    setError("");
    try {
      await authClient.revokeSession({ target });
      await refresh();
    } catch (err) {
      setError(message(err));
    }
  }
  const others = list.data?.sessions.filter((s) => !s.current) ?? [];
  return (
    <Section
      title="Devices"
      description="Where your account is signed in. Signing a device out ends its session at once."
    >
      <ErrorLine error={error} />
      {list.data?.sessions.map((s) => (
        <div
          key={s.id}
          className="flex items-center justify-between gap-3 rounded-lg border p-3 text-sm"
        >
          <div>
            <p className="font-medium">
              {s.userAgent || "Unknown browser"}{" "}
              {s.current && <Badge variant="secondary">This device</Badge>}
            </p>
            <p className="text-xs text-muted-foreground">
              {s.ip} · last active {when(s.lastSeenAt)}
            </p>
          </div>
          {!s.current && (
            <Button variant="ghost" size="sm" onClick={() => revoke({ case: "id", value: s.id })}>
              Sign out
            </Button>
          )}
        </div>
      ))}
      {others.length > 1 && (
        <Button
          variant="outline"
          className="self-start"
          onClick={() => revoke({ case: "allOthers", value: true })}
        >
          Sign out every other device
        </Button>
      )}
    </Section>
  );
}

const activityNames: Record<string, string> = {
  signin: "Signed in",
  "signin.failed": "Failed sign-in",
  "signin.first_factor": "First sign-in step",
  reauth: "Confirmed it's you",
  "reauth.failed": "Failed confirmation",
  "session.signout": "Signed out",
  "session.revoke": "Signed a device out",
  "passkey.add": "Added a passkey",
  "passkey.remove": "Removed a passkey",
  "passkey.rename": "Renamed a passkey",
  "totp.enable": "Turned on the authenticator app",
  "totp.disable": "Turned off the authenticator app",
  "recovery_codes.regenerate": "Made new recovery codes",
  "oauth.link": "Linked an account",
  "oauth.unlink": "Unlinked an account",
  "ssh_key.add": "Added an SSH key",
  "ssh_key.remove": "Removed an SSH key",
  "email.change": "Changed your email",
};

export function Activity() {
  const [pages, setPages] = useState<string[]>([""]);
  return (
    <Section
      title="Activity"
      description="Sign-ins, failed attempts, and changes to how your account is secured."
    >
      <div className="flex flex-col divide-y text-sm">
        {pages.map((token, i) => (
          <ActivityPage
            key={token || "first"}
            token={token}
            last={i === pages.length - 1}
            onMore={(t) => setPages([...pages, t])}
          />
        ))}
      </div>
    </Section>
  );
}

function ActivityPage({
  token,
  last,
  onMore,
}: {
  token: string;
  last: boolean;
  onMore: (t: string) => void;
}) {
  const page = useQuery(AuthService.method.listActivity, { pageToken: token });
  return (
    <>
      {page.data?.events.map((e) => {
        const meta = JSON.parse(e.metadataJson || "{}") as Record<string, unknown>;
        const detail = [meta.method, meta.name, meta.provider].filter(Boolean).join(" · ");
        return (
          <div key={e.id} className="flex justify-between gap-4 py-2">
            <span className={e.action.endsWith(".failed") ? "text-destructive" : undefined}>
              {activityNames[e.action] ?? e.action}
              {detail && <span className="text-muted-foreground"> · {detail}</span>}
              {meta.new_device === true && <Badge className="ml-2">New device</Badge>}
            </span>
            <span className="shrink-0 text-xs text-muted-foreground">
              {when(e.at)} · {e.ip}
            </span>
          </div>
        );
      })}
      {last && page.data?.nextPageToken && (
        <Button
          variant="ghost"
          size="sm"
          className="mt-2 self-start"
          onClick={() => onMore(page.data.nextPageToken)}
        >
          Show more
        </Button>
      )}
    </>
  );
}

// Fingerprint shows a passkey's fingerprint as nodes print it, computed
// here from its public key.
function Fingerprint({ publicKey }: { publicKey: Uint8Array }) {
  const [fp, setFp] = useState("");
  useEffect(() => {
    keyFingerprint(publicKey).then(setFp);
  }, [publicKey]);
  return <code>{fp}</code>;
}

export function SSHKeys() {
  const list = useQuery(AuthService.method.listSSHKeys, {});
  const withReauth = useReauth();
  const refresh = useRefresh();
  const [key, setKey] = useState("");
  const [error, setError] = useState("");
  const username = list.data?.sftpUsername;

  async function add(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await withReauth(() => authClient.addSSHKey({ publicKey: key }));
      setKey("");
      await refresh();
    } catch (err) {
      setError(message(err));
    }
  }

  async function remove(id: string) {
    setError("");
    try {
      await withReauth(() => authClient.deleteSSHKey({ id }));
      await refresh();
    } catch (err) {
      setError(message(err));
    }
  }

  return (
    <Section
      title="SSH keys"
      description="For SFTP, the way to move large files to and from your servers. SFTP takes SSH keys only; there are no passwords."
    >
      <ErrorLine error={error} />
      {username && (
        <p className="text-sm">
          Your SFTP username is <code className="font-semibold">{username}</code>. Connect as{" "}
          <code>{username}.&lt;server short ID&gt;@n-&lt;node&gt;.raptornodes.net</code> on the port
          the node shows (2022 by default).
        </p>
      )}
      {list.data?.keys.map((k) => (
        <div
          key={k.id}
          className="flex items-center justify-between gap-3 rounded-lg border p-3 text-sm"
        >
          <div>
            <p className="font-medium">{k.name}</p>
            <p className="text-xs text-muted-foreground">
              {k.type} · <code>{k.fingerprint}</code> · last used {when(k.lastUsedAt)}
            </p>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={`Remove ${k.name}`}
            onClick={() => remove(k.id)}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      <form onSubmit={add} className="flex flex-col gap-2">
        <Label htmlFor="ssh-key">Add a public key (the contents of ~/.ssh/id_ed25519.pub)</Label>
        <textarea
          id="ssh-key"
          required
          rows={3}
          className="rounded-md border bg-background p-2 font-mono text-xs"
          placeholder="ssh-ed25519 AAAA… you@laptop"
          value={key}
          onChange={(e) => setKey(e.target.value)}
        />
        <Button type="submit" className="self-start">
          Add SSH key
        </Button>
      </form>
    </Section>
  );
}
