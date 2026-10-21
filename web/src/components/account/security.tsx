import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  KeyRound,
  Link2,
  LogIn,
  LogOut,
  type LucideIcon,
  Mail,
  Monitor,
  Plus,
  ShieldAlert,
  ShieldCheck,
  Smartphone,
  Trash2,
} from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { encode } from "uqr";
import {
  ActivityCard,
  type FilterOptions,
  type Filters,
  type TimelineItem,
  type TimelinePage,
  toFilter,
} from "@/components/activity-timeline";
import { useConfirm } from "@/components/confirm";
import { ProviderIcon } from "@/components/provider-icons";
import { useReauth } from "@/components/reauth";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import type { AuditEvent } from "@/gen/raptor/panel/v1/audit_pb";
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { keyFingerprint } from "@/lib/canonical";
import { message } from "@/lib/errors";
import { dateTime, ago as since } from "@/lib/format";
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

function when(ts: { seconds: bigint; nanos: number } | undefined): string {
  return ts ? dateTime(timestampDate(ts as Parameters<typeof timestampDate>[0])) : "never";
}

function Section({
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

function ErrorLine({ error }: { error: string }) {
  return error ? <p className="text-sm text-destructive">{error}</p> : null;
}

function useRefresh() {
  const client = useQueryClient();
  return () => client.invalidateQueries();
}

export function Passkeys() {
  const list = useQuery(AuthService.method.listPasskeys, {});
  const ask = useConfirm();
  const withReauth = useReauth();
  const refresh = useRefresh();
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [addError, setAddError] = useState("");
  const [busy, setBusy] = useState(false);
  const passkeys = list.data?.passkeys ?? [];

  function openAdd() {
    setName("");
    setAddError("");
    setAdding(true);
  }

  async function add(e: FormEvent) {
    e.preventDefault();
    setAddError("");
    setBusy(true);
    try {
      const begin = await withReauth(() => authClient.beginPasskeyRegistration({}));
      if (!begin.challenge) throw new Error("The Panel didn't send a challenge.");
      const credentialJson = await createPasskey(begin.challenge.optionsJson);
      await authClient.finishPasskeyRegistration({
        answer: { ceremonyId: begin.challenge.ceremonyId, credentialJson },
        name,
      });
      setAdding(false);
      await refresh();
    } catch (err) {
      if (!passkeyCancelled(err)) setAddError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string, label: string) {
    const last = passkeys.length === 1;
    const ok = await ask.confirm({
      title: `Remove "${label}"?`,
      description: last
        ? "It's your only passkey. Until you add another, you can't confirm dangerous actions on your servers, like deleting one or restoring a backup. It stays on your device or in your password manager until you delete it there too."
        : "You can't sign in or confirm dangerous actions with it anymore; your other passkeys keep working. It stays on your device or in your password manager until you delete it there too.",
      confirm: "Remove passkey",
      destructive: true,
    });
    if (!ok) return;
    setError("");
    try {
      await withReauth(() => authClient.deletePasskey({ id }));
      await refresh();
    } catch (err) {
      setError(message(err));
    }
  }

  async function rename(id: string, current: string) {
    const next = await ask.prompt({
      title: "Rename the passkey",
      description: "A name that tells you which device it's on.",
      label: "Name",
      initial: current,
      maxLength: 64,
      confirm: "Rename",
    });
    if (next === null || next === current) return;
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
      {list.data && passkeys.length === 0 && (
        <p className="text-sm text-muted-foreground">No passkeys yet.</p>
      )}
      {passkeys.map((p) => (
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
            onClick={() => remove(p.id, p.name)}
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      {passkeysSupported() ? (
        <Button className="self-start" onClick={openAdd}>
          <Plus /> Add a passkey
        </Button>
      ) : (
        <p className="text-sm text-muted-foreground">This browser can't use passkeys.</p>
      )}
      <Dialog open={adding} onOpenChange={(open) => !busy && setAdding(open)}>
        <DialogContent>
          <form onSubmit={add} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Add a passkey</DialogTitle>
              <DialogDescription>
                Your browser or password manager asks to make one, with your fingerprint, face, or
                device PIN. Name it after the device it's on, so you can tell your passkeys apart.
              </DialogDescription>
            </DialogHeader>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="passkey-name">Name</Label>
              <Input
                id="passkey-name"
                autoFocus
                placeholder="MacBook, iPhone, 1Password"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={64}
              />
            </div>
            <ErrorLine error={addError} />
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                disabled={busy}
                onClick={() => setAdding(false)}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                {busy ? "Waiting for your passkey…" : "Add passkey"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </Section>
  );
}

function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const text = `Raptor recovery codes. Each works once.\n\n${codes.join("\n")}\n`;
  const [copied, setCopied] = useState(false);
  return (
    <>
      <DialogHeader>
        <DialogTitle>Save your recovery codes</DialogTitle>
        <DialogDescription>
          Each one signs you in once if you lose the phone with your authenticator app. Keep them
          somewhere safe, like your password manager: they won't be shown again.
        </DialogDescription>
      </DialogHeader>
      <pre className="grid grid-cols-2 gap-x-6 gap-y-1 rounded-lg border bg-muted/40 p-4 font-mono text-sm">
        {codes.map((c) => (
          <span key={c}>{c}</span>
        ))}
      </pre>
      <DialogFooter className="sm:justify-between">
        <div className="flex gap-2">
          <Button
            variant="outline"
            onClick={async () => {
              await navigator.clipboard.writeText(text);
              setCopied(true);
            }}
          >
            {copied ? "Copied" : "Copy"}
          </Button>
          <Button
            variant="outline"
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
        </div>
        <Button onClick={onDone}>I've saved them</Button>
      </DialogFooter>
    </>
  );
}

export function Authenticator() {
  const session = useQuery(AuthService.method.getSession, {});
  const ask = useConfirm();
  const withReauth = useReauth();
  const refresh = useRefresh();
  const [setup, setSetup] = useState<{ secret: string; url: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const [setupError, setSetupError] = useState("");
  const [busy, setBusy] = useState(false);
  const on = session.data?.user?.totpEnabled ?? false;

  async function act(fn: () => Promise<void>) {
    setError("");
    try {
      await fn();
    } catch (err) {
      setError(message(err));
    }
  }

  function start() {
    act(async () => {
      const res = await withReauth(() => authClient.beginTOTPSetup({}));
      setCode("");
      setSetupError("");
      setSetup({ secret: res.secret, url: res.url });
    });
  }

  async function finish(e: FormEvent) {
    e.preventDefault();
    setSetupError("");
    setBusy(true);
    try {
      const res = await withReauth(() => authClient.finishTOTPSetup({ code }));
      setSetup(null);
      setCodes(res.recoveryCodes);
      await refresh();
    } catch (err) {
      setSetupError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function newCodes() {
    const ok = await ask.confirm({
      title: "Make new recovery codes?",
      description: "Your current recovery codes stop working at once, used or not.",
      confirm: "Make new codes",
    });
    if (!ok) return;
    act(async () => {
      const res = await withReauth(() => authClient.regenerateRecoveryCodes({}));
      setCodes(res.recoveryCodes);
      await refresh();
    });
  }

  async function turnOff() {
    const ok = await ask.confirm({
      title: "Turn off the authenticator app?",
      description:
        "Signing in with an email code or with Google, GitHub, or Discord stops asking for a code from your app, and your recovery codes stop working. Your passkeys aren't affected.",
      confirm: "Turn off",
      destructive: true,
    });
    if (!ok) return;
    act(async () => {
      await withReauth(() => authClient.disableTOTP({}));
      await refresh();
    });
  }

  return (
    <Section
      title="Authenticator app"
      description="After an email or Google/GitHub/Discord sign-in, also ask for a code from an app like 1Password, Google Authenticator, or Aegis."
    >
      <ErrorLine error={error} />
      {on ? (
        <>
          <p className="flex items-center gap-2 text-sm">
            <Smartphone className="size-4" /> On. {session.data?.recoveryCodesLeft} recovery codes
            left.
          </p>
          <div className="flex gap-2">
            <Button variant="outline" onClick={newCodes}>
              New recovery codes
            </Button>
            <Button variant="destructive" onClick={turnOff}>
              Turn off
            </Button>
          </div>
        </>
      ) : (
        <Button className="self-start" onClick={start}>
          Set up
        </Button>
      )}

      {/* Setting up, then the recovery codes it makes, in one dialog. The
          codes are shown once, so only "I've saved them" closes it then. */}
      <Dialog
        open={setup !== null || codes !== null}
        onOpenChange={(open) => {
          if (!open && !codes && !busy) setSetup(null);
        }}
      >
        <DialogContent showCloseButton={!codes}>
          {codes ? (
            <RecoveryCodes codes={codes} onDone={() => setCodes(null)} />
          ) : (
            setup && (
              <form onSubmit={finish} className="flex flex-col gap-4">
                <DialogHeader>
                  <DialogTitle>Set up your authenticator app</DialogTitle>
                  <DialogDescription>
                    Scan this with your authenticator app, or type the key into it. Then enter the
                    6-digit code it shows.
                  </DialogDescription>
                </DialogHeader>
                <div className="flex flex-col items-center gap-3">
                  {/* The QR code is made here: the secret never leaves the page. */}
                  <QRCode value={setup.url} className="w-48 rounded-lg bg-white p-2" />
                  <code className="break-all text-center font-mono text-xs text-muted-foreground">
                    {setup.secret}
                  </code>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="totp-code">The 6-digit code it shows</Label>
                  <Input
                    id="totp-code"
                    autoFocus
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    required
                    maxLength={6}
                    className="font-mono tracking-widest"
                    value={code}
                    onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                  />
                </div>
                <ErrorLine error={setupError} />
                <DialogFooter>
                  <Button
                    type="button"
                    variant="outline"
                    disabled={busy}
                    onClick={() => setSetup(null)}
                  >
                    Cancel
                  </Button>
                  <Button type="submit" disabled={busy || code.length !== 6}>
                    Turn on
                  </Button>
                </DialogFooter>
              </form>
            )
          )}
        </DialogContent>
      </Dialog>
    </Section>
  );
}

export function LinkedAccounts() {
  const methods = useQuery(AuthService.method.getSignInMethods, {});
  const list = useQuery(AuthService.method.listOAuthAccounts, {});
  const ask = useConfirm();
  const withReauth = useReauth();
  const refresh = useRefresh();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const accounts = list.data?.accounts ?? [];
  // Every provider this Panel offers, and any linked one it no longer does.
  const providers = [
    ...new Set([...(methods.data?.oauthProviders ?? []), ...accounts.map((a) => a.provider)]),
  ];
  if (methods.data && providers.length === 0) return null;

  async function run(provider: string, fn: () => Promise<void>) {
    setBusy(provider);
    setError("");
    try {
      await fn();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Connections</CardTitle>
        <CardDescription>Connect an account elsewhere to sign in with it.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="divide-y">
          {providers.map((p) => {
            const linked = accounts.find((a) => a.provider === p);
            const name = providerNames[p] ?? p;
            return (
              <div
                key={p}
                className="flex items-center justify-between gap-4 py-3 first:pt-0 last:pb-0"
              >
                <div className="flex min-w-0 items-center gap-3">
                  <ProviderIcon provider={p} className="size-5 shrink-0" />
                  <div className="min-w-0">
                    <p className="text-sm font-medium">{name}</p>
                    <p className="truncate text-xs text-muted-foreground">
                      {linked ? linked.email || "Connected" : "Not connected"}
                    </p>
                  </div>
                </div>
                <div className="flex shrink-0 items-center gap-3">
                  {!list.data ? (
                    <Skeleton className="h-7 w-24" />
                  ) : linked ? (
                    <>
                      <span className="hidden items-center gap-1.5 text-xs text-muted-foreground sm:flex">
                        <span className="size-2 rounded-full bg-ok" />
                        Connected
                      </span>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy !== ""}
                        onClick={async () => {
                          const ok = await ask.confirm({
                            title: `Disconnect ${name}?`,
                            description: `You can't sign in with ${name} anymore. Connect it again any time.`,
                            confirm: "Disconnect",
                            destructive: true,
                          });
                          if (!ok) return;
                          run(p, async () => {
                            await withReauth(() =>
                              authClient.unlinkOAuthAccount({ id: linked.id }),
                            );
                            await refresh();
                          });
                        }}
                      >
                        Disconnect
                      </Button>
                    </>
                  ) : (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={busy !== "" || !methods.data?.oauthProviders.includes(p)}
                      onClick={() =>
                        run(p, async () => {
                          const res = await withReauth(() =>
                            authClient.beginOAuth({ provider: p, link: true }),
                          );
                          window.location.assign(res.url);
                        })
                      }
                    >
                      Connect
                    </Button>
                  )}
                </div>
              </div>
            );
          })}
        </div>
        <ErrorLine error={error} />
      </CardContent>
    </Card>
  );
}

// describeDevice turns a user agent into "Chrome on macOS".
export function describeDevice(ua: string): string {
  if (!ua) return "Unknown device";
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /OPR\//.test(ua)
      ? "Opera"
      : /Firefox\//.test(ua)
        ? "Firefox"
        : /Chrome\//.test(ua)
          ? "Chrome"
          : /Safari\//.test(ua)
            ? "Safari"
            : "";
  const os = /iPhone|iPad/.test(ua)
    ? "iOS"
    : /Android/.test(ua)
      ? "Android"
      : /Mac OS X|Macintosh/.test(ua)
        ? "macOS"
        : /Windows/.test(ua)
          ? "Windows"
          : /Linux/.test(ua)
            ? "Linux"
            : "";
  if (browser && os) return `${browser} on ${os}`;
  return browser || os || ua.slice(0, 60);
}

function DeviceIcon({ ua }: { ua: string }) {
  const Icon = /iPhone|Android.*Mobile/.test(ua) ? Smartphone : Monitor;
  return <Icon className="size-4" />;
}

export function Devices() {
  const list = useQuery(AuthService.method.listSessions, {});
  const ask = useConfirm();
  const refresh = useRefresh();
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [includeThis, setIncludeThis] = useState(false);
  const [busy, setBusy] = useState(false);
  const sessions = list.data?.sessions ?? [];
  // This device and the most recently active few; the rest on request.
  const [all, setAll] = useState(false);
  const shownMax = 6;
  const shown = all ? sessions : sessions.slice(0, shownMax);

  async function signOut(id: string, device: string) {
    const ok = await ask.confirm({
      title: `Sign out ${device}?`,
      description:
        "Its session ends at once, and it has to sign in again. If you don't recognise it, sign it out, then check your passkeys and connections.",
      confirm: "Sign out",
      destructive: true,
    });
    if (!ok) return;
    setError("");
    try {
      await authClient.revokeSession({ target: { case: "id", value: id } });
      await refresh();
    } catch (err) {
      setError(message(err));
    }
  }

  async function signOutHere() {
    const ok = await ask.confirm({
      title: "Sign out of this device?",
      description: "You'll go back to the sign-in page. Your other devices stay signed in.",
      confirm: "Sign out",
    });
    if (!ok) return;
    setError("");
    try {
      await authClient.signOut({});
      window.location.assign("/signin");
    } catch (err) {
      setError(message(err));
    }
  }

  async function signOutAll() {
    setBusy(true);
    setError("");
    try {
      await authClient.revokeSession({ target: { case: "allOthers", value: true } });
      if (includeThis) {
        await authClient.signOut({});
        window.location.assign("/signin");
        return;
      }
      await refresh();
      setConfirming(false);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 flex-1 basis-xs space-y-1.5">
          <CardTitle>Devices</CardTitle>
          <CardDescription>
            Where your account is signed in. Signing a device out ends its session at once.
          </CardDescription>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            setIncludeThis(false);
            setConfirming(true);
          }}
        >
          <LogOut /> Sign out of all devices
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <ErrorLine error={error} />
        <div className="divide-y">
          {shown.map((s) => (
            <div
              key={s.id}
              className="flex items-center justify-between gap-4 py-3 first:pt-0 last:pb-0"
            >
              <div className="flex min-w-0 items-center gap-3">
                <span className="flex size-8 shrink-0 items-center justify-center rounded-lg border text-muted-foreground">
                  <DeviceIcon ua={s.userAgent} />
                </span>
                <div className="min-w-0">
                  <p className="flex items-center gap-2 text-sm font-medium">
                    {describeDevice(s.userAgent)}
                    {s.current && <Badge variant="secondary">This device</Badge>}
                  </p>
                  <p className="truncate text-xs text-muted-foreground">
                    {[s.ip, s.current ? "active now" : `last active ${ago(s.lastSeenAt)}`]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                </div>
              </div>
              <Button
                size="sm"
                variant="ghost"
                onClick={() =>
                  s.current ? signOutHere() : signOut(s.id, describeDevice(s.userAgent))
                }
              >
                Sign out
              </Button>
            </div>
          ))}
        </div>
        {sessions.length > shownMax && (
          <Button variant="ghost" size="sm" className="self-start" onClick={() => setAll(!all)}>
            {all ? "Show fewer" : `Show all ${sessions.length} devices`}
          </Button>
        )}
      </CardContent>
      <Dialog open={confirming} onOpenChange={setConfirming}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Sign out of all devices?</DialogTitle>
            <DialogDescription>
              Every other device where your account is signed in is signed out at once. Do this if
              you lost a device or think someone else got in.
            </DialogDescription>
          </DialogHeader>
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={includeThis} onCheckedChange={(on) => setIncludeThis(on)} />
            Sign out of this device too
          </label>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirming(false)}>
              Cancel
            </Button>
            <Button variant="destructive" disabled={busy} onClick={signOutAll}>
              Sign out
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

function ago(ts: { seconds: bigint; nanos: number } | undefined): string {
  return ts ? since(timestampDate(ts as Parameters<typeof timestampDate>[0])) : "never";
}

const methodNames: Record<string, string> = {
  passkey: "a passkey",
  email: "an email code",
  link: "an email link",
  totp: "your authenticator app",
  recovery: "a recovery code",
  google: "Google",
  github: "GitHub",
  discord: "Discord",
};

type Meta = Record<string, unknown>;

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

// describeEvent is one line of the account's activity, in plain words, and
// its icon.
function describeEvent(action: string, meta: Meta): { text: string; icon: LucideIcon } {
  const method = methodNames[str(meta.method)] ?? str(meta.method);
  const via = method ? ` with ${method}` : "";
  const name = str(meta.name);
  const provider = providerNames[str(meta.provider)] ?? str(meta.provider);
  switch (action) {
    case "signin":
      return { text: `Signed in${via}`, icon: LogIn };
    case "signin.first_factor":
      return { text: `Started signing in${via}`, icon: LogIn };
    case "signin.failed":
      return { text: `Someone failed to sign in${via}`, icon: ShieldAlert };
    case "reauth":
      return { text: `Confirmed it's you${via}`, icon: ShieldCheck };
    case "reauth.failed":
      return { text: "Failed to confirm it's you", icon: ShieldAlert };
    case "session.signout":
      return { text: "Signed out", icon: LogOut };
    case "session.revoke":
      return { text: "Signed a device out", icon: LogOut };
    case "passkey.add":
      return { text: name ? `Added the passkey "${name}"` : "Added a passkey", icon: KeyRound };
    case "passkey.remove":
      return { text: name ? `Removed the passkey "${name}"` : "Removed a passkey", icon: KeyRound };
    case "passkey.rename":
      return {
        text: name ? `Renamed a passkey to "${name}"` : "Renamed a passkey",
        icon: KeyRound,
      };
    case "totp.enable":
      return { text: "Turned on the authenticator app", icon: Smartphone };
    case "totp.disable":
      return { text: "Turned off the authenticator app", icon: Smartphone };
    case "recovery_codes.regenerate":
      return { text: "Made new recovery codes", icon: ShieldCheck };
    case "oauth.link":
      return { text: provider ? `Connected ${provider}` : "Connected an account", icon: Link2 };
    case "oauth.unlink":
      return {
        text: provider ? `Disconnected ${provider}` : "Disconnected an account",
        icon: Link2,
      };
    case "ssh_key.add":
      return { text: name ? `Added the SSH key "${name}"` : "Added an SSH key", icon: KeyRound };
    case "ssh_key.remove":
      return {
        text: name ? `Removed the SSH key "${name}"` : "Removed an SSH key",
        icon: KeyRound,
      };
    case "email.change":
      return { text: `Changed your email to ${str(meta.to)}`, icon: Mail };
    default:
      // Something this version of the app has no words for yet.
      return { text: "Changed something on your account", icon: Activity };
  }
}

const accountFilters: FilterOptions = {
  kinds: [
    { label: "Sign-ins and sign-outs", actions: ["signin%", "reauth%", "session.%"] },
    { label: "Passkeys and two-factor", actions: ["passkey.%", "totp.%", "recovery_codes.%"] },
    { label: "Connected accounts", actions: ["oauth.%"] },
    { label: "SSH keys", actions: ["ssh_key.%"] },
    { label: "Email address", actions: ["email.%"] },
  ],
  failures: true,
};

async function accountPage(token: string, f: Filters): Promise<TimelinePage<AuditEvent>> {
  const res = await authClient.listActivity({
    pageToken: token,
    filter: toFilter(f, accountFilters),
  });
  return { events: res.events, next: res.nextPageToken };
}

function accountItem(e: AuditEvent): TimelineItem {
  const meta = JSON.parse(e.metadataJson || "{}") as Meta;
  const { text, icon } = describeEvent(e.action, meta);
  return {
    id: e.id,
    at: e.at ? timestampDate(e.at) : undefined,
    icon,
    text,
    failed: e.action.endsWith(".failed"),
    badge: meta.new_device === true && (
      <Badge variant="outline" className="ml-2">
        New device
      </Badge>
    ),
    details: [e.userAgent && describeDevice(e.userAgent)],
    ip: e.ip,
  };
}

export function ActivityLog() {
  return (
    <ActivityCard
      description="Sign-ins, failed attempts, and changes to how your account is secured."
      queryKey={["account-activity"]}
      fetchPage={accountPage}
      toItem={accountItem}
      filters={accountFilters}
    />
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

// QRCode draws a QR code as SVG, from uqr's matrix: no markup is inserted,
// so it needs no Trusted Types policy.
function QRCode({ value, className }: { value: string; className?: string }) {
  const qr = encode(value, { border: 1 });
  const path = qr.data
    .flatMap((row, y) => row.flatMap((dark, x) => (dark ? [`M${x} ${y}h1v1h-1z`] : [])))
    .join("");
  return (
    <svg
      viewBox={`0 0 ${qr.size} ${qr.size}`}
      className={className}
      shapeRendering="crispEdges"
      role="img"
      aria-label="QR code for your authenticator app"
    >
      <path d={path} fill="#000" />
    </svg>
  );
}
