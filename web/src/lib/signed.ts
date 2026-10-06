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
  // The passkeys that may sign (the user's; one, to pair it).
  allow: Uint8Array[];
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
  const signature = await signChallenge(await commandHash(fields), opts.allow);
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
