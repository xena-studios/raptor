import { hmac } from "@noble/hashes/hmac.js";
import { sha256 } from "@noble/hashes/sha2.js";
import * as Crypto from "expo-crypto";
import * as SecureStore from "expo-secure-store";

import type { PhoneApproval } from "@/gen/raptor/panel/v1/auth_pb";
import { authClient } from "@/lib/transport";

// Approving sign-ins from this phone (docs/PANEL.md#phone-approvals): the
// person picks, here, the number the browser shows. The phone proves it's
// this phone with a secret only it holds, kept in the Keychain or Keystore
// behind Face ID (readable only after Face ID, and gone if the phone's
// Face ID or fingerprints change). The Panel keeps a sealed copy and checks
// an HMAC over each request's challenge, so this app's tokens alone can't
// approve anything.

const key = "raptor.approver";
const store = (prompt: string): SecureStore.SecureStoreOptions => ({
  keychainService: "raptor.approver",
  requireAuthentication: true,
  authenticationPrompt: prompt,
});

export function canHoldSecret(): boolean {
  try {
    return SecureStore.canUseBiometricAuthentication();
  } catch {
    return false;
  }
}

// enroll makes this phone an approver.
export async function enroll() {
  const secret = Crypto.getRandomBytes(32);
  let b = "";
  for (const x of secret) b += String.fromCharCode(x);
  await SecureStore.setItemAsync(key, btoa(b), store("Turn on approving sign-ins"));
  try {
    await authClient.enrollPhoneApprover({ secret });
  } catch (err) {
    await SecureStore.deleteItemAsync(key, { keychainService: "raptor.approver" }).catch(() => {});
    throw err;
  }
}

// unenroll stops this phone approving.
export async function unenroll() {
  await authClient.removePhoneApprover({});
  await SecureStore.deleteItemAsync(key, { keychainService: "raptor.approver" }).catch(() => {});
}

function b64url(bytes: Uint8Array): string {
  let s = "";
  for (const x of bytes) s += String.fromCharCode(x);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// LostSecret: the secret can't be read (Face ID changed): approving needs
// setting up again, by signing this phone in again.
export class LostSecret extends Error {}

// decide answers a request: approve with the number picked, or deny. Face
// ID unlocks the secret first.
export async function decide(a: PhoneApproval, approve: boolean, number: number): Promise<string> {
  const stored = await SecureStore.getItemAsync(
    key,
    store(approve ? "Approve the sign-in" : "Deny the sign-in"),
  );
  if (!stored) throw new LostSecret("This phone can't approve any more: its Face ID changed.");
  const secret = Uint8Array.from(atob(stored), (ch) => ch.charCodeAt(0));
  const text = [
    "raptor approval v1",
    a.id,
    b64url(a.challenge),
    approve ? "approve" : "deny",
    String(approve ? number : 0),
  ].join("\n");
  const mac = hmac(sha256, secret, new TextEncoder().encode(text));
  const res = await authClient.decidePhoneApproval({
    approvalId: a.id,
    approve,
    number: approve ? number : 0,
    mac,
  });
  return res.status;
}
