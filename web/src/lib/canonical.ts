// The command a passkey signs, in the same canonical form Wings checks
// (internal/shared/nodecmd: JSON Canonicalization Scheme, RFC 8785). Kept
// free of imports so `node --test` can check it against the Go vector.

// canonicalJSON serializes like RFC 8785: object keys sorted by UTF-16 code
// units (JavaScript's default sort), no whitespace, and ECMAScript number
// and string formatting (JSON.stringify's).
export function canonicalJSON(v: unknown): string {
  if (v === null || typeof v !== "object") {
    if (typeof v === "number" && !Number.isFinite(v))
      throw new Error("JSON has no NaN or Infinity");
    return JSON.stringify(v);
  }
  if (Array.isArray(v)) return `[${v.map(canonicalJSON).join(",")}]`;
  const o = v as Record<string, unknown>;
  const keys = Object.keys(o)
    .filter((k) => o[k] !== undefined)
    .sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${canonicalJSON(o[k])}`).join(",")}}`;
}

export type CommandFields = {
  action: string;
  commandId: string;
  expiresAt: number; // unix seconds
  nodeId: string;
  params?: Record<string, unknown>;
  serverId: string;
  userId: string;
};

// commandCanonical is exactly what's hashed for the passkey to sign.
export function commandCanonical(c: CommandFields): string {
  return canonicalJSON({
    action: c.action,
    command_id: c.commandId,
    expires_at: c.expiresAt,
    node_id: c.nodeId,
    params: c.params ?? {},
    server_id: c.serverId,
    user_id: c.userId,
  });
}

// commandHash is SHA-256 of the canonical form: the WebAuthn challenge.
export async function commandHash(c: CommandFields): Promise<Uint8Array> {
  const bytes = new TextEncoder().encode(commandCanonical(c));
  return new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
}

// uuidv7 is a time-ordered random ID, as Wings wants for command IDs.
export function uuidv7(now = Date.now()): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  for (let i = 0; i < 6; i++) b[i] = Math.floor(now / 2 ** (8 * (5 - i))) & 0xff;
  b[6] = ((b[6] ?? 0) & 0x0f) | 0x70;
  b[8] = ((b[8] ?? 0) & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

// keyFingerprint is how people compare a passkey between the browser and
// the node (Wings' KeyFingerprint): the first 80 bits of SHA-256 of its
// COSE public key, base32, in groups of four.
export async function keyFingerprint(cose: Uint8Array): Promise<string> {
  const sum = new Uint8Array(await crypto.subtle.digest("SHA-256", cose.slice()));
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0;
  let value = 0;
  let out = "";
  for (const byte of sum) {
    value = (value << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      out += alphabet[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
    if (out.length >= 20) break;
  }
  return (out.slice(0, 20).match(/.{4}/g) ?? []).join("-");
}

const b64 = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes));

export type PinFields = {
  joinToken: string;
  credentialId: Uint8Array;
  publicKey: Uint8Array;
  userId: string;
  name: string;
};

// pinCanonical is what the owner's passkey signs to be trusted by a node
// that links with joinToken (nodecmd.OwnerPin): byte fields in standard
// base64, as Go writes them.
export async function pinCanonical(p: PinFields): Promise<string> {
  const tokenHash = new Uint8Array(
    await crypto.subtle.digest("SHA-256", new TextEncoder().encode(p.joinToken)),
  );
  return canonicalJSON({
    purpose: "raptor.owner_pin.v1",
    join_token_hash: b64(tokenHash),
    credential_id: b64(p.credentialId),
    public_key: b64(p.publicKey),
    user_id: p.userId,
    name: p.name,
  });
}

export async function pinHash(p: PinFields): Promise<Uint8Array> {
  const bytes = new TextEncoder().encode(await pinCanonical(p));
  return new Uint8Array(await crypto.subtle.digest("SHA-256", bytes));
}
