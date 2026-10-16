import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useRouter } from "@tanstack/react-router";
import { Monitor, Moon, Sun } from "lucide-react";
import { type FormEvent, useState } from "react";

import { DetailList } from "@/components/page";
import { useReauth } from "@/components/reauth";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
import type { User } from "@/gen/raptor/panel/v1/auth_pb";
import { message } from "@/lib/errors";
import { dateOnly } from "@/lib/format";
import {
  setTheme,
  setThemeStyle,
  type Theme,
  type ThemeStyle,
  useTheme,
  useThemeStyle,
} from "@/lib/theme";
import { authClient } from "@/lib/transport";
import { cn } from "@/lib/utils";

// Profile is the account's name and email.
export function Profile({ user }: { user: User }) {
  const router = useRouter();
  const [name, setName] = useState(user.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const dirty = name.trim() !== user.name;

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      await authClient.updateProfile({ name });
      await router.invalidate();
      setSaved(true);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Profile</CardTitle>
        <CardDescription>Your name, and the email you sign in with.</CardDescription>
      </CardHeader>
      <form onSubmit={save}>
        <CardContent className="flex flex-col gap-4">
          <div className="grid gap-2">
            <Label htmlFor="account-name">Name</Label>
            <Input
              id="account-name"
              placeholder="Your name"
              maxLength={64}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setSaved(false);
              }}
            />
          </div>
          <div className="grid gap-2">
            <Label>Email</Label>
            <div className="flex items-center justify-between gap-3 rounded-lg border px-3 py-1.5">
              <span className="truncate text-sm">{user.email}</span>
              <ChangeEmail current={user.email} />
            </div>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </CardContent>
        <CardFooter className="mt-4 gap-3">
          <Button type="submit" disabled={!dirty || busy}>
            Save
          </Button>
          {saved && <span className="text-sm text-muted-foreground">Saved.</span>}
        </CardFooter>
      </form>
    </Card>
  );
}

// ChangeEmail moves the account to a new address: a code goes to it, and
// the change happens once it's typed in.
function ChangeEmail({ current }: { current: string }) {
  const router = useRouter();
  const withReauth = useReauth();
  const [open, setOpen] = useState(false);
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [sent, setSent] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function reset(next: boolean) {
    setOpen(next);
    if (!next) {
      setEmail("");
      setCode("");
      setSent(false);
      setError("");
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (!sent) {
        await withReauth(() => authClient.startEmailChange({ newEmail: email }));
        setSent(true);
      } else {
        await authClient.finishEmailChange({ newEmail: email, code });
        await router.invalidate();
        reset(false);
      }
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Button type="button" size="sm" variant="outline" onClick={() => setOpen(true)}>
        Change
      </Button>
      <Dialog open={open} onOpenChange={reset}>
        <DialogContent>
          <form onSubmit={submit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Change your email</DialogTitle>
              <DialogDescription>
                {sent
                  ? `Type the code we sent to ${email.trim()}. ${current} will be told about the change.`
                  : "We'll send a code to the new address to check it's yours."}
              </DialogDescription>
            </DialogHeader>
            {!sent ? (
              <div className="grid gap-2">
                <Label htmlFor="new-email">New email</Label>
                <Input
                  id="new-email"
                  type="email"
                  autoComplete="email"
                  required
                  autoFocus
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                />
              </div>
            ) : (
              <div className="grid gap-2">
                <Label htmlFor="email-code">Code</Label>
                <Input
                  id="email-code"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  required
                  autoFocus
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
              </div>
            )}
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => reset(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                {sent ? "Change email" : "Send code"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </>
  );
}

const themeOptions: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: "system", label: "System", icon: Monitor },
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
];

const styleOptions: { value: ThemeStyle; label: string; note: string }[] = [
  { value: "standard", label: "Standard", note: "Raptor's colours." },
  {
    value: "contrast",
    label: "High contrast",
    note: "Stronger text, borders, and focus, for low vision.",
  },
  { value: "calm", label: "Calm", note: "Neutral greys; red only where it matters." },
];

// Appearance picks the theme's mode and style, here and on the account, so
// they follow the user to other browsers.
export function Appearance() {
  const theme = useTheme();
  const style = useThemeStyle();
  const [error, setError] = useState("");

  async function save(change: { theme?: Theme; themeStyle?: ThemeStyle }) {
    setError("");
    try {
      await authClient.updateProfile(change);
    } catch (err) {
      setError(`Couldn't save it to your account: ${message(err)}`);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Appearance</CardTitle>
        <CardDescription>
          How Raptor looks. It's saved to your account, so your other browsers use it too.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-2 text-sm font-medium">Mode</legend>
          <div className="grid grid-cols-3 gap-2 sm:max-w-md">
            {themeOptions.map(({ value, label, icon: Icon }) => (
              <button
                key={value}
                type="button"
                aria-pressed={theme === value}
                onClick={() => {
                  setTheme(value);
                  void save({ theme: value });
                }}
                className={cn(
                  "flex flex-col items-center gap-2 rounded-lg border p-3 text-sm transition-colors hover:bg-muted",
                  theme === value && "border-primary bg-muted",
                )}
              >
                <Icon className="size-5" />
                {label}
              </button>
            ))}
          </div>
        </fieldset>
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-2 text-sm font-medium">Style</legend>
          <div className="grid gap-2 sm:grid-cols-3">
            {styleOptions.map(({ value, label, note }) => (
              <button
                key={value}
                type="button"
                aria-pressed={style === value}
                onClick={() => {
                  setThemeStyle(value);
                  void save({ themeStyle: value });
                }}
                className={cn(
                  "flex flex-col items-start gap-2 rounded-lg border p-3 text-left text-sm transition-colors hover:bg-muted",
                  style === value && "border-primary bg-muted",
                )}
              >
                <StyleSwatch style={value} />
                <span className="font-medium">{label}</span>
                <span className="text-xs text-muted-foreground">{note}</span>
              </button>
            ))}
          </div>
        </fieldset>
        {error && <p className="text-sm text-destructive">{error}</p>}
      </CardContent>
    </Card>
  );
}

// Each style's page, red, text, and secondary text, light and dark, as in
// index.css: StyleSwatch draws them whatever the page is in now.
const swatches: Record<ThemeStyle, Record<"light" | "dark", [string, string, string, string]>> = {
  standard: {
    light: [
      "oklch(0.975 0.004 25)",
      "oklch(0.545 0.215 25)",
      "oklch(0.18 0.018 25)",
      "oklch(0.46 0.013 25)",
    ],
    dark: [
      "oklch(0.145 0.012 25)",
      "oklch(0.66 0.19 25)",
      "oklch(0.96 0.005 25)",
      "oklch(0.68 0.011 25)",
    ],
  },
  contrast: {
    light: ["oklch(1 0 0)", "oklch(0.46 0.19 25)", "oklch(0.12 0 0)", "oklch(0.3 0 0)"],
    dark: ["oklch(0.08 0 0)", "oklch(0.76 0.16 25)", "oklch(1 0 0)", "oklch(0.88 0 0)"],
  },
  calm: {
    light: ["oklch(0.975 0 0)", "oklch(0.52 0.15 25)", "oklch(0.2 0 0)", "oklch(0.45 0 0)"],
    dark: ["oklch(0.16 0 0)", "oklch(0.64 0.13 25)", "oklch(0.92 0 0)", "oklch(0.68 0 0)"],
  },
};

// StyleSwatch is a tiny picture of a style in the current mode: its page,
// its red, and two lines of text.
function StyleSwatch({ style }: { style: ThemeStyle }) {
  useTheme();
  const mode = document.documentElement.classList.contains("dark") ? "dark" : "light";
  const [page, red, text, muted] = swatches[style][mode];
  return (
    <span
      aria-hidden
      className="flex h-10 w-full items-center gap-2 rounded-md border px-2"
      style={{ background: page, borderColor: style === "contrast" ? text : undefined }}
    >
      <span className="h-5 w-8 rounded-sm" style={{ background: red }} />
      <span className="flex flex-1 flex-col gap-1">
        <span className="h-1.5 w-4/5 rounded-full" style={{ background: text }} />
        <span className="h-1.5 w-1/2 rounded-full" style={{ background: muted }} />
      </span>
    </span>
  );
}

// Details are the account's facts.
export function Details({ user }: { user: User }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Details</CardTitle>
        <CardDescription>About your account.</CardDescription>
      </CardHeader>
      <CardContent>
        <DetailList
          rows={[
            ["Account ID", <code key="id">{user.id}</code>],
            ["Member since", user.createdAt ? dateOnly(timestampDate(user.createdAt)) : ""],
          ]}
        />
      </CardContent>
    </Card>
  );
}

// DeleteAccount deletes the account after "confirm it's you".
export function DeleteAccount({ email }: { email: string }) {
  const withReauth = useReauth();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function remove(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await withReauth(() => authClient.deleteAccount({}));
      window.location.assign("/signin");
    } catch (err) {
      setError(message(err));
      setBusy(false);
    }
  }

  return (
    <Card className="border-destructive/40">
      <CardHeader>
        <CardTitle>Delete account</CardTitle>
        <CardDescription>
          Deletes your account, its passkeys, and its sign-ins, and takes you out of every org. An
          org you're the only owner of needs another owner first, or to be deleted. This can't be
          undone.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Button variant="destructive" onClick={() => setOpen(true)}>
          Delete account
        </Button>
      </CardContent>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          setTyped("");
          setError("");
        }}
      >
        <DialogContent>
          <form onSubmit={remove} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>Delete your account?</DialogTitle>
              <DialogDescription>
                Type your email, <span className="font-medium">{email}</span>, to confirm.
              </DialogDescription>
            </DialogHeader>
            <Input
              aria-label="Your email"
              autoFocus
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
            />
            {error && <p className="text-sm text-destructive">{error}</p>}
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button
                type="submit"
                variant="destructive"
                disabled={busy || typed.trim().toLowerCase() !== email}
              >
                Delete account
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </Card>
  );
}
