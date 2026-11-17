import * as Crypto from "expo-crypto";
import * as Device from "expo-device";
import * as SecureStore from "expo-secure-store";
import * as WebBrowser from "expo-web-browser";
import { useSyncExternalStore } from "react";

import { apiURL, appURL, clientId, redirectURI } from "@/lib/config";

// Signing in (docs/PANEL.md#the-phone-app): the app opens the web app in
// the system browser, where the person signs in the usual way and approves
// the app; the browser comes back with a one-time code, which the app
// trades for an access token (an hour) and a refresh token with its PKCE
// verifier. The refresh token works once: each refresh gives a new one,
// and using an old one again ends the session, so refreshes go one at a
// time. Only the refresh token is stored (in the Keychain or Keystore);
// the access token lives in memory.

export type AuthState =
  | { status: "loading" }
  | { status: "signedOut"; error?: string }
  | { status: "signedIn" };

const refreshKey = "raptor.refresh";
const store: SecureStore.SecureStoreOptions = {
  keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK,
};

let state: AuthState = { status: "loading" };
const listeners = new Set<() => void>();
let access: { token: string; until: number } | null = null;
let refreshing: Promise<string> | null = null;

function set(next: AuthState) {
  state = next;
  for (const l of listeners) l();
}

export function useAuth(): AuthState {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => state,
  );
}

// SignedOutError: the session is over (signed out elsewhere, or a refresh
// token used twice); the app goes back to the sign-in screen.
export class SignedOutError extends Error {}

function base64url(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function form(values: Record<string, string>): string {
  return Object.entries(values)
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
    .join("&");
}

function query(url: string): Record<string, string> {
  const out: Record<string, string> = {};
  const q = url.split("?")[1]?.split("#")[0] ?? "";
  for (const pair of q.split("&")) {
    if (!pair) continue;
    const [k, v = ""] = pair.split("=");
    out[decodeURIComponent(k)] = decodeURIComponent(v.replace(/\+/g, " "));
  }
  return out;
}

type Tokens = { access_token: string; refresh_token: string; expires_in: number };

// token calls POST /oauth/token. A 400 means the grant is no good (used,
// expired, or the session ended); anything else is worth trying again.
async function token(values: Record<string, string>): Promise<Tokens> {
  const res = await fetch(`${apiURL}/oauth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form({ client_id: clientId, ...values }),
  });
  const body = await res.json().catch(() => ({}));
  if (res.status === 400 || res.status === 401) {
    throw new SignedOutError(body.error_description ?? "Signed out");
  }
  if (!res.ok) throw new Error(body.error_description ?? `The Panel answered ${res.status}`);
  return body as Tokens;
}

async function keep(t: Tokens) {
  access = { token: t.access_token, until: Date.now() + t.expires_in * 1000 };
  await SecureStore.setItemAsync(refreshKey, t.refresh_token, store);
}

async function forget() {
  access = null;
  await SecureStore.deleteItemAsync(refreshKey, store).catch(() => {});
}

function refresh(): Promise<string> {
  refreshing ??= (async () => {
    try {
      const rt = await SecureStore.getItemAsync(refreshKey, store);
      if (!rt) throw new SignedOutError("Signed out");
      const t = await token({ grant_type: "refresh_token", refresh_token: rt });
      await keep(t);
      return t.access_token;
    } catch (err) {
      if (err instanceof SignedOutError) {
        await forget();
        set({ status: "signedOut", error: "You were signed out. Sign in again." });
      }
      throw err;
    } finally {
      refreshing = null;
    }
  })();
  return refreshing;
}

// accessToken is a token good for at least another minute.
export async function accessToken(): Promise<string> {
  if (access && access.until - 60_000 > Date.now()) return access.token;
  return refresh();
}

// expire drops the access token after the Panel refused it, so the next
// call refreshes.
export function expire(rejected: string) {
  if (access?.token === rejected) access = null;
}

// start runs once at launch: signed in if there's a refresh token. Offline
// counts as signed in; the first request refreshes when it can.
export async function start() {
  const rt = await SecureStore.getItemAsync(refreshKey, store).catch(() => null);
  if (!rt) {
    set({ status: "signedOut" });
    return;
  }
  try {
    await refresh();
    set({ status: "signedIn" });
  } catch (err) {
    if (!(err instanceof SignedOutError)) set({ status: "signedIn" });
  }
}

// signIn opens the web app to approve this phone. It resolves false if the
// person cancelled.
export async function signIn(): Promise<boolean> {
  const verifier = base64url(Crypto.getRandomBytes(32));
  const challenge = (
    await Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, verifier, {
      encoding: Crypto.CryptoEncoding.BASE64,
    })
  )
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
  const st = base64url(Crypto.getRandomBytes(16));
  const device = Device.deviceName ?? Device.modelName ?? "Phone";
  const url = `${appURL}/authorize?${form({
    client_id: clientId,
    redirect_uri: redirectURI,
    code_challenge: challenge,
    code_challenge_method: "S256",
    state: st,
    device,
  })}`;
  const res = await WebBrowser.openAuthSessionAsync(url, redirectURI);
  if (res.type !== "success") return false;
  const back = query(res.url);
  if (back.state !== st)
    throw new Error("That sign-in wasn't the one this app started. Try again.");
  if (back.error === "access_denied") return false;
  if (!back.code) throw new Error("Signing in didn't finish. Try again.");
  try {
    await keep(
      await token({
        grant_type: "authorization_code",
        code: back.code,
        code_verifier: verifier,
        redirect_uri: redirectURI,
      }),
    );
  } catch (err) {
    if (err instanceof SignedOutError) throw new Error("That sign-in expired. Try again.");
    throw err;
  }
  set({ status: "signedIn" });
  return true;
}

// signOut ends the session on the Panel (end, if it can be reached) and
// forgets it here either way.
export async function signOut(end?: () => Promise<unknown>) {
  try {
    await end?.();
  } catch {
    // Offline or already ended: forgetting it here is what matters.
  }
  await forget();
  set({ status: "signedOut" });
}
