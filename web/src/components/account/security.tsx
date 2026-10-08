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
  ShieldAlert,
  ShieldCheck,
  Smartphone,
  Trash2,
} from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { renderSVG } from "uqr";

import { ProviderIcon } from "@/components/provider-icons";
import { useReauth } from "@/components/reauth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { AuthService } from "@/gen/raptor/panel/v1/auth_pb";
import { keyFingerprint } from "@/lib/canonical";
import { message } from "@/lib/errors";
import { authClient } from "@/lib/transport";
import { cn } from "@/lib/utils";
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
                        <span className="size-2 rounded-full bg-emerald-500" />
                        Connected
                      </span>
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={busy !== ""}
                        onClick={() =>
                          run(p, async () => {
                            await withReauth(() =>
                              authClient.unlinkOAuthAccount({ id: linked.id }),
                            );
                            await refresh();
                          })
                        }
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
function describeDevice(ua: string): string {
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
  const refresh = useRefresh();
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [includeThis, setIncludeThis] = useState(false);
  const [busy, setBusy] = useState(false);
  const sessions = list.data?.sessions ?? [];

  async function signOut(id: string) {
    setError("");
    try {
      await authClient.revokeSession({ target: { case: "id", value: id } });
      await refresh();
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
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="space-y-1.5">
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
          {sessions.map((s) => (
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
              {!s.current && (
                <Button size="sm" variant="ghost" onClick={() => signOut(s.id)}>
                  Sign out
                </Button>
              )}
            </div>
          ))}
        </div>
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
            <input
              type="checkbox"
              checked={includeThis}
              onChange={(e) => setIncludeThis(e.target.checked)}
            />
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

// ago is "5 minutes ago", "yesterday", or the date.
export function ago(ts: { seconds: bigint; nanos: number } | undefined): string {
  if (!ts) return "never";
  const d = timestampDate(ts as Parameters<typeof timestampDate>[0]);
  const s = Math.floor((Date.now() - d.getTime()) / 1000);
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} minute${m === 1 ? "" : "s"} ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} hour${h === 1 ? "" : "s"} ago`;
  const days = Math.floor(h / 24);
  if (days === 1) return "yesterday";
  if (days < 7) return `${days} days ago`;
  return d.toLocaleDateString();
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
    default: {
      const phrase = action.replace(/[._]/g, " ");
      return { text: phrase.charAt(0).toUpperCase() + phrase.slice(1), icon: Activity };
    }
  }
}

function dayLabel(d: Date): string {
  const today = new Date();
  const yesterday = new Date();
  yesterday.setDate(today.getDate() - 1);
  if (d.toDateString() === today.toDateString()) return "Today";
  if (d.toDateString() === yesterday.toDateString()) return "Yesterday";
  return d.toLocaleDateString(undefined, {
    weekday: "long",
    month: "long",
    day: "numeric",
    year: d.getFullYear() === today.getFullYear() ? undefined : "numeric",
  });
}

export function ActivityLog() {
  const [pages, setPages] = useState<string[]>([""]);
  return (
    <Card>
      <CardHeader>
        <CardTitle>Activity</CardTitle>
        <CardDescription>
          Sign-ins, failed attempts, and changes to how your account is secured.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {pages.map((token, i) => (
          <ActivityPage
            key={token || "first"}
            token={token}
            last={i === pages.length - 1}
            onMore={(t) => setPages([...pages, t])}
          />
        ))}
      </CardContent>
    </Card>
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
  const events = page.data?.events ?? [];
  if (page.data && events.length === 0 && !token) {
    return <p className="text-sm text-muted-foreground">Nothing yet.</p>;
  }
  // Grouped by day, newest first.
  const days: { label: string; events: typeof events }[] = [];
  for (const e of events) {
    const label = e.at ? dayLabel(timestampDate(e.at)) : "";
    const lastDay = days[days.length - 1];
    if (lastDay && lastDay.label === label) lastDay.events.push(e);
    else days.push({ label, events: [e] });
  }
  return (
    <>
      {days.map((day) => (
        <section key={`${token}-${day.label}`} className="flex flex-col gap-3">
          <h3 className="text-xs font-medium text-muted-foreground">{day.label}</h3>
          <ol>
            {day.events.map((e, i) => {
              const meta = JSON.parse(e.metadataJson || "{}") as Meta;
              const { text, icon: Icon } = describeEvent(e.action, meta);
              const failed = e.action.endsWith(".failed");
              const at = e.at ? timestampDate(e.at) : undefined;
              return (
                <li key={e.id} className="relative flex gap-3 pb-4 last:pb-0">
                  {i < day.events.length - 1 && (
                    <span
                      aria-hidden
                      className="absolute top-8 -bottom-0 left-4 w-px -translate-x-1/2 bg-border"
                    />
                  )}
                  <span
                    className={cn(
                      "relative flex size-8 shrink-0 items-center justify-center rounded-lg border bg-card",
                      failed ? "text-destructive" : "text-muted-foreground",
                    )}
                  >
                    <Icon className="size-4" />
                  </span>
                  <div className="min-w-0 flex-1 pt-1">
                    <p className={cn("text-sm", failed && "text-destructive")}>
                      {text}
                      {meta.new_device === true && (
                        <Badge variant="outline" className="ml-2">
                          New device
                        </Badge>
                      )}
                    </p>
                    <p className="mt-0.5 truncate text-xs text-muted-foreground">
                      <time title={at?.toLocaleString()}>
                        {at?.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}
                      </time>
                      {[e.userAgent && describeDevice(e.userAgent), e.ip]
                        .filter(Boolean)
                        .map((x) => ` · ${x}`)
                        .join("")}
                    </p>
                  </div>
                </li>
              );
            })}
          </ol>
        </section>
      ))}
      {last && page.data?.nextPageToken && (
        <Button
          variant="outline"
          size="sm"
          className="self-start"
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
