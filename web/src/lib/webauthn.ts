// WebAuthn in the browser: the Panel sends options as JSON
// (PublicKeyCredential*OptionsJSON) and wants the credential back as JSON
// (PublicKeyCredential.toJSON()). Converted by hand, since not every
// browser has parse*OptionsFromJSON and toJSON yet.

const b64url = {
  decode(s: string): ArrayBuffer {
    const b64 = s
      .replace(/-/g, "+")
      .replace(/_/g, "/")
      .padEnd(Math.ceil(s.length / 4) * 4, "=");
    const bin = atob(b64);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out.buffer;
  },
  encode(buf: ArrayBuffer | ArrayBufferView | null | undefined): string {
    if (!buf) return "";
    const bytes =
      buf instanceof ArrayBuffer
        ? new Uint8Array(buf)
        : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
    let bin = "";
    for (const b of bytes) bin += String.fromCharCode(b);
    return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  },
};

type Descriptor = { id: string; type: "public-key"; transports?: AuthenticatorTransport[] };

function descriptors(list: Descriptor[] | undefined): PublicKeyCredentialDescriptor[] | undefined {
  return list?.map((d) => ({ ...d, id: b64url.decode(d.id) }));
}

export function passkeysSupported(): boolean {
  return (
    typeof window !== "undefined" && "PublicKeyCredential" in window && !!navigator.credentials
  );
}

// createPasskey runs navigator.credentials.create with the Panel's
// options and returns the answer for FinishPasskeyRegistration.
export async function createPasskey(optionsJSON: string): Promise<string> {
  const o = JSON.parse(optionsJSON);
  const publicKey: PublicKeyCredentialCreationOptions = {
    ...o,
    challenge: b64url.decode(o.challenge),
    user: { ...o.user, id: b64url.decode(o.user.id) },
    excludeCredentials: descriptors(o.excludeCredentials),
  };
  const cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was created.");
  const r = cred.response as AuthenticatorAttestationResponse;
  return JSON.stringify({
    id: cred.id,
    rawId: b64url.encode(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    response: {
      attestationObject: b64url.encode(r.attestationObject),
      clientDataJSON: b64url.encode(r.clientDataJSON),
      transports: r.getTransports?.() ?? [],
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  });
}

// getPasskey runs navigator.credentials.get with the Panel's options and
// returns the answer for FinishPasskeySignIn or FinishReauth.
export async function getPasskey(optionsJSON: string): Promise<string> {
  const o = JSON.parse(optionsJSON);
  const publicKey: PublicKeyCredentialRequestOptions = {
    ...o,
    challenge: b64url.decode(o.challenge),
    allowCredentials: descriptors(o.allowCredentials),
  };
  const cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey answered.");
  const r = cred.response as AuthenticatorAssertionResponse;
  return JSON.stringify({
    id: cred.id,
    rawId: b64url.encode(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    response: {
      authenticatorData: b64url.encode(r.authenticatorData),
      clientDataJSON: b64url.encode(r.clientDataJSON),
      signature: b64url.encode(r.signature),
      userHandle: b64url.encode(r.userHandle),
    },
    clientExtensionResults: cred.getClientExtensionResults(),
  });
}

// passkeyCancelled is true when the user closed the browser's prompt.
export function passkeyCancelled(err: unknown): boolean {
  return (
    err instanceof DOMException && (err.name === "NotAllowedError" || err.name === "AbortError")
  );
}

export type Assertion = {
  credentialId: Uint8Array;
  authenticatorData: Uint8Array;
  clientDataJson: Uint8Array;
  signature: Uint8Array;
};

// signChallenge asks a passkey to sign a challenge the app made itself
// (a command's hash), with the app's hostname as the RP ID and the user
// verifying (fingerprint, face, or PIN). allow limits it to the user's
// passkeys, or to one.
export async function signChallenge(
  challenge: Uint8Array,
  allow: Uint8Array[],
): Promise<Assertion> {
  const cred = (await navigator.credentials.get({
    publicKey: {
      challenge: challenge.slice().buffer,
      rpId: window.location.hostname,
      userVerification: "required",
      timeout: 5 * 60_000,
      allowCredentials: allow.map((id) => ({ type: "public-key" as const, id: id.slice().buffer })),
    },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey answered.");
  const r = cred.response as AuthenticatorAssertionResponse;
  return {
    credentialId: new Uint8Array(cred.rawId),
    authenticatorData: new Uint8Array(r.authenticatorData),
    clientDataJson: new Uint8Array(r.clientDataJSON),
    signature: new Uint8Array(r.signature),
  };
}
