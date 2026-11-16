import * as LocalAuthentication from "expo-local-authentication";
import * as SecureStore from "expo-secure-store";
import { useSyncExternalStore } from "react";
import { AppState, Platform } from "react-native";

// The app lock: when it's on, the app asks for Face ID (or Touch ID, a
// fingerprint, or the phone's passcode) when it starts, and when it comes
// back after a minute away. While the app is in the background its screen
// is covered, so the app switcher doesn't show servers or alerts. The
// choice is this phone's own, kept in the Keychain or Keystore.

const key = "raptor.lock";
const away = 60_000;

// loaded: the choice has been read (until then the splash screen stays).
type LockState = { loaded: boolean; enabled: boolean; locked: boolean; covered: boolean };

let state: LockState = { loaded: false, enabled: false, locked: false, covered: false };
const listeners = new Set<() => void>();
let leftAt = 0;

function set(next: Partial<LockState>) {
  state = { ...state, ...next };
  for (const l of listeners) l();
}

export function useLock(): LockState {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => state,
  );
}

// method is what the phone will ask for, in words: "Face ID".
export async function method(): Promise<string> {
  const enrolled = await LocalAuthentication.isEnrolledAsync().catch(() => false);
  if (!enrolled) return "your passcode";
  const types = await LocalAuthentication.supportedAuthenticationTypesAsync().catch(
    (): LocalAuthentication.AuthenticationType[] => [],
  );
  const face = types.includes(LocalAuthentication.AuthenticationType.FACIAL_RECOGNITION);
  if (Platform.OS === "ios") return face ? "Face ID" : "Touch ID";
  return face ? "face unlock" : "your fingerprint";
}

// available says whether the phone can lock at all: it needs at least a
// passcode.
export async function available(): Promise<boolean> {
  const level = await LocalAuthentication.getEnrolledLevelAsync().catch(
    () => LocalAuthentication.SecurityLevel.NONE,
  );
  return level !== LocalAuthentication.SecurityLevel.NONE;
}

async function check(prompt: string): Promise<boolean> {
  const res = await LocalAuthentication.authenticateAsync({
    promptMessage: prompt,
    cancelLabel: "Cancel",
  });
  return res.success;
}

// unlock asks for Face ID (or whatever the phone has) and opens the app.
export async function unlock(): Promise<boolean> {
  const ok = await check("Unlock Raptor");
  if (ok) set({ locked: false });
  return ok;
}

// setEnabled turns the lock on (after checking it works, so nobody locks
// themselves out) or off.
export async function setEnabled(on: boolean): Promise<boolean> {
  if (!(await check(on ? "Turn on the app lock" : "Turn off the app lock"))) return false;
  if (on) await SecureStore.setItemAsync(key, "1");
  else await SecureStore.deleteItemAsync(key);
  set({ enabled: on, locked: false });
  return true;
}

// startLock reads the choice and locks the app if it's on. It runs once at
// launch.
export async function startLock() {
  const on = (await SecureStore.getItemAsync(key).catch(() => null)) === "1";
  set({ loaded: true, enabled: on, locked: on });
  AppState.addEventListener("change", (s) => {
    if (s === "background") {
      leftAt = Date.now();
      set({ covered: state.enabled });
    } else if (s === "inactive") {
      // The app switcher: cover it. (Face ID's own prompt also makes the
      // app inactive, but it's already covered by the lock screen then.)
      if (!state.locked) set({ covered: state.enabled });
    } else if (s === "active") {
      const relock = state.enabled && leftAt > 0 && Date.now() - leftAt > away;
      leftAt = 0;
      set({ covered: false, locked: state.locked || relock });
    }
  });
}

// forget turns the lock off without asking, when the person signs out.
export async function forgetLock() {
  await SecureStore.deleteItemAsync(key).catch(() => {});
  set({ enabled: false, locked: false, covered: false });
}
