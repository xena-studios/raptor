import { type CommandFields, commandHash, uuidv7 } from "@/lib/canonical";
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
