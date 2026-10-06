import { type CommandFields, commandHash, keyFingerprint, pinHash, uuidv7 } from "@/lib/canonical";
import { commandClient } from "@/lib/transport";
import { signChallenge } from "@/lib/webauthn";

// How long a signed command is good for: long enough to reach a node that's
// reconnecting, well under Wings' 10-minute limit.
const lifetimeSeconds = 5 * 60;

// sendSigned runs a dangerous action: the user's passkey signs the exact
// command (docs/SECURITY-MODEL.md#passkey-signed-commands), which Wings
// checks itself, so the Panel relays it but can't forge or change it.
export async function sendSigned(opts: {
  userId: string;
  nodeId: string;
  action: string;
  serverId?: string;
  params?: Record<string, unknown>;
  // The passkey that must sign (to pair it); any otherwise, and Wings
  // refuses keys it doesn't trust.
  expect?: Uint8Array;
}) {
  const fields: CommandFields = {
    action: opts.action,
    commandId: uuidv7(),
    expiresAt: Math.floor(Date.now() / 1000) + lifetimeSeconds,
    nodeId: opts.nodeId,
    params: opts.params,
    serverId: opts.serverId ?? "",
    userId: opts.userId,
  };
  const signature = await signChallenge(await commandHash(fields));
  if (opts.expect && !sameBytes(signature.credentialId, opts.expect)) {
    throw new Error("A different passkey answered. Pick the same one both times.");
  }
  return commandClient.execute({
    nodeId: fields.nodeId,
    action: fields.action,
    serverId: fields.serverId,
    paramsJson: JSON.stringify(fields.params ?? {}),
    commandId: fields.commandId,
    expiresAt: BigInt(fields.expiresAt),
    signature,
  });
}

export function sameBytes(a: Uint8Array, b: Uint8Array): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

// whichPasskey asks the user's password manager for a passkey and returns
// the credential ID that answered: a signature over random bytes, used for
// nothing else.
export async function whichPasskey(): Promise<Uint8Array> {
  const a = await signChallenge(crypto.getRandomValues(new Uint8Array(32)));
  return a.credentialId;
}

const b64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));

// signPin has one of the user's passkeys sign that a node linking with
// joinToken should trust it (nodecmd.OwnerPin), and returns the pin to hand
// the Panel and the fingerprint the node will print.
export async function signPin(opts: {
  joinToken: string;
  userId: string;
  passkeys: { credentialId: Uint8Array; publicKey: Uint8Array; name: string }[];
}): Promise<{ pinJson: string; fingerprint: string; name: string }> {
  const id = await whichPasskey();
  const key = opts.passkeys.find((p) => sameBytes(p.credentialId, id));
  if (!key)
    throw new Error("That passkey isn't on your Raptor account. Add it under Security first.");
  const fields = {
    joinToken: opts.joinToken,
    credentialId: key.credentialId,
    publicKey: key.publicKey,
    userId: opts.userId,
    name: key.name,
  };
  const sig = await signChallenge(await pinHash(fields));
  if (!sameBytes(sig.credentialId, key.credentialId)) {
    throw new Error("A different passkey answered. Pick the same one both times.");
  }
  const tokenHash = new Uint8Array(
    await crypto.subtle.digest("SHA-256", new TextEncoder().encode(opts.joinToken)),
  );
  const pinJson = JSON.stringify({
    join_token_hash: b64(tokenHash),
    credential_id: b64(key.credentialId),
    public_key: b64(key.publicKey),
    user_id: opts.userId,
    name: key.name,
    signature: {
      credential_id: b64(sig.credentialId),
      authenticator_data: b64(sig.authenticatorData),
      client_data_json: b64(sig.clientDataJson),
      signature: b64(sig.signature),
    },
  });
  return { pinJson, fingerprint: await keyFingerprint(key.publicKey), name: key.name };
}
