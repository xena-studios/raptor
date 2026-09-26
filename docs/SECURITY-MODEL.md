# Security Model

## The core risk

**The Panel can send commands to Wings, and Wings runs as root on every customer's box.** A compromise of the Panel must not become root access on every node. Most rules below exist to limit that blast radius.

The strongest of them: **destructive and code-changing actions must be signed by the user's own passkey, and Wings checks that signature itself** ([Passkey-signed commands](#passkey-signed-commands)). Even an attacker in full control of the Panel can't delete servers, plant a malicious egg, or grant themselves access without a real person's passkey for each action.

## Assets

1. Customer nodes (root on their hardware)
2. Game server data (worlds, configs, player data)
3. Backups
4. User accounts and sessions
5. The Wings release signing key
6. The Panel's signing key (proves the Panel to nodes and signs per-user command grants)
7. The trusted passkey keys pinned on each node (they authorize destructive and code-changing actions)
8. The web app bundle (it's what asks users' passkeys to sign)

## Trust boundaries

```
 Internet ──► Cloudflare ──► Panel (API) ◄══WebSocket (opened by Wings)══ Wings (root) ──► Docker ──► container
                                                          │
                                   owner's root / CLI ────┘
```

| Boundary | Rule |
|---|---|
| Panel → Wings | **Typed commands only.** No "run this shell command" operation exists in the protocol, for anyone, including staff. |
| User → Wings (via Panel) | Every command carries a **signed, short-lived grant** bound to user + server + action. Wings verifies it. |
| User → Wings, dangerous actions | The command must also carry the **user's passkey signature** over the exact command, from a key the node trusts. Wings verifies it; the Panel can't forge it. |
| Container → host | Everything inside a container is contained. Everything Wings does **on the host** with server-controlled data is hostile input. |
| Owner root → Wings | Root on the box owns the box. We don't defend against the owner. |
| Wings → Panel | **Wings reports are untrusted.** Never used for billing or security decisions about other tenants. |

## Rules

### Wings
- **No arbitrary execution.** The node connection protocol, local socket API, and support tooling expose only typed operations.
- **Host-side file safety.** Every file operation on server data (file manager, SFTP, config parsers, backups, restores, imports) goes through **`os.Root`**, confined to the server's directory. Symlinks, `..`, and absolute paths that escape are rejected. This is where Pterodactyl's Wings has had repeated vulnerabilities, so it gets the most review and fuzzing.
- **Variables are validated against egg rules before substitution**, and never pass through a host shell.
- **Container hardening:** non-root user, capabilities dropped, `no-new-privileges`, seccomp, only the server directory mounted, PID limits.
- **Install containers** are treated as the least trusted code on the node (egg scripts, root inside the container, internet access). They run separately from the runtime container, mount only the server directory (read-write) and the script (read-only), are never privileged, have limits and a timeout, and are **network-isolated**: outbound internet only, with the host, private ranges, other servers, and the cloud metadata endpoint blocked. The post-install ownership fix never follows symlinks. Details in [EGGS.md](EGGS.md#install).
- **Every Raptor container** (install and runtime) is blocked from the cloud metadata endpoint (`169.254.169.254`), which can hand out provider credentials.
- Wings runs as root (required for Docker, quotas, and nftables) with systemd sandboxing where possible (`ProtectSystem`, `ProtectHome`, `PrivateTmp`, restricted address families).
- **Docker firewall:** Wings publishes only allocated ports. Its own rules live in a separate nftables table (`inet raptor`) that runs before Docker's chains, is replaced atomically, and is reapplied if something removes it. See [WINGS.md](WINGS.md#firewall).
- **Wings never touches what it doesn't own:** containers and networks without the `raptor.wings.managed` label are never listed, modified, or removed, even when their names collide with Wings' own.
- Local socket: root + `raptor` group only.

### Enrollment and identity
- Join tokens: single-use, 1-hour expiry, org-bound, stored hashed.
- **Keys are generated on the node and never leave it.** The Panel stores only the public key. The node proves itself on every connection by signing a fresh challenge (bound to a timestamp and the connection's purpose, so it can't be replayed).
- **The Panel proves itself** with its signing key, which Wings pins at enrollment. TLS terminates at Cloudflare, so the TLS certificate alone doesn't prove the Panel's identity to Wings.
- Node removal revokes the node's key and drops its connection immediately.
- SFTP host keys are generated on the node; the fingerprint is reported to the Panel over the authenticated connection and shown to users.
- **The Panel's signing key** is kept separate from the Panel's application secrets, ideally in an HSM or KMS later, at minimum a separate encrypted secret with restricted access.
- The Panel origin only accepts connections from Cloudflare, and trusts `CF-Connecting-IP` only on those.
- **Cloudflare can read Panel traffic** (it terminates TLS): console, commands, and web file transfers. Disclosed in the privacy policy; SFTP is direct to the node.

### Passkey-signed commands

A WebAuthn passkey signs whatever challenge a site gives it. For dangerous actions the challenge is **the hash of the exact command**, so the signature authorizes that command and nothing else, and Wings verifies it without trusting the Panel.

**Flow**
1. The user clicks e.g. "Delete server *smp*". The web app builds the command: node, server, action, parameters, `command_id` (UUIDv7), and an expiry 5 minutes out.
2. The browser asks the user's passkey to sign `SHA-256(canonical command)`, with the user verifying by fingerprint, face, or PIN. The canonical form is the command as JSON under the JSON Canonicalization Scheme (RFC 8785), so the browser and Wings produce identical bytes without depending on protobuf's encoding.
3. The Panel forwards the command, the signature, and WebAuthn's `authenticatorData` and `clientDataJSON` to Wings. Changing any part of the command breaks the signature.
4. **Wings checks**:
   - the signature, with a key it trusts for that user;
   - `clientDataJSON.type` is `webauthn.get` and the challenge equals the command's hash;
   - the origin is `https://raptorpanel.net` and the `rpIdHash` matches `raptorpanel.net`, so phishing sites can't produce valid signatures;
   - both user presence and user verification flags are set;
   - the command hasn't expired;
   - its `command_id` has never run (the `executed_commands` table);
   - the signature counter moved forward, when the authenticator keeps one.

   Anything missing or invalid is rejected and logged, even though it came from the Panel.

Verification takes about 0.1 ms on the node. The user's cost is one fingerprint or PIN tap, which replaces the re-authentication prompt these actions already needed. Bulk actions sign once (one signature over the list of servers), and the browser signs before sending, so there's no extra round trip.

**Which actions are signed** (anything that destroys data, changes what code runs, or changes who has access):
- Deleting a server; reinstalling with "wipe"; restoring a backup over current files
- Changing the egg, install script, startup command, or Docker image
- Granting support access
- Adding SSH/SFTP keys or sub-users, and delegating signed actions to them
- Changing the node's trusted keys
- Removing the node

**Not signed** (to keep everyday use fast): start/stop/restart/kill, console commands, file browsing and editing, schedules, and settings that don't change code or access. These still need the Panel's per-user grant. A compromised Panel could read files and the console and disrupt servers, but not destroy them, backdoor them through eggs or images, or quietly add access.

**Trusted keys: rooted on the node, not in the Panel**

If the Panel simply told Wings which keys to trust, a compromised Panel would send its own. So the set of trusted keys lives in Wings' SQLite and only changes by rules Wings enforces:
- **At enrollment** the owner signs the enrollment (including the join token) with their passkey, and that key is pinned on the node. `raptor bootstrap` prints the key's fingerprint and the browser shows the same fingerprint; owners are told to compare them. This is trust-on-first-use: a Panel that was already compromised at the moment of enrollment could substitute a key, and comparing fingerprints catches that.
- **Adding or removing a trusted key** (a new device, another org owner) must be signed by a key the node already trusts.
- **Sub-users** get dangerous actions only through a **delegation signed by an owner's passkey**: "user X's key may reinstall server S". The Panel can't create one. Delegations can expire and be revoked (also signed).
- **Recovery without the Panel:** if every trusted passkey is lost, root on the box runs `raptor keys reset`. It prints a one-time pairing code; the owner enters it in the Panel and signs the pairing with a new passkey, and Wings pins that key. The code is only ever shown on the box, so a compromised Panel can't complete a pairing by itself. Root on the box owns the box, as the trust boundaries already say.
- `raptor keys list` shows the trusted keys and delegations on the node.

**The web app is part of the trust boundary.** Browsers don't show *what* a passkey is signing, so an attacker who controls the JavaScript could display "Restart" while asking for a signature on "Delete". Defenses:
- The web app is **served from separate static hosting, not the API servers**. Compromising the API or the database can't change the code users run. Deploying the web app requires separate credentials.
- A **strict Content Security Policy** (no inline scripts, no third-party scripts, `script-src` limited to the app's own hashed bundles, Trusted Types).
- **Reproducible builds:** each release publishes the bundle hashes, so anyone can check that the deployed app matches the public source.
- **Alerts straight from the node:** Wings reports every signed dangerous action to the owner through a channel configured on the node (Discord webhook or email), not through the Panel, and `raptor audit` lists them from the node's own records. A tampered action gets noticed even if the Panel hides it.

Even if every one of these failed, each malicious action would still need a real person's passkey at that moment, bound to one command. That turns "one Panel breach controls every node" into "an attacker must trick specific users, one action at a time".

**Requirements**
- Dangerous actions need a **passkey**. Authenticator-app codes can't be verified by Wings, so they're not enough for these actions (they still protect sign-in). Nearly every current phone and laptop supports passkeys, and hardware security keys cover the rest.
- On by default for every node, with no Panel-side way to turn it off. Only root on the box can change it (`raptor keys` commands).

### Releases and supply chain
- Wings releases are **signed** with minisign (Ed25519): the signature covers `checksums.txt`, which covers every binary. CI only builds **draft** releases; the maintainer signs and publishes locally. The private key is kept **offline** and never stored in the repository or CI.
- The install script is generated per release with the binary's SHA-256 embedded, and verifies it before running anything. The binary verifies every later update's minisign signature with an embedded public key.
- Dependencies pinned. CI runs `govulncheck`, `npm audit`, license checks, and secret scanning (gitleaks).
- Egg imports show the image registry and install script before import.

### Panel
- **Passwordless auth built into the Panel** (passkeys, OAuth, email codes; TOTP 2FA). No passwords exist to leak. Passkeys are phishing-resistant and preferred. Details in [PANEL.md](PANEL.md#auth).
- OAuth logins only link to existing accounts when the provider verified the email (prevents account takeover through unverified emails).
- The Panel issues its own sessions (hashed tokens, `HttpOnly`, `Secure`, `SameSite` cookies), with CSRF protection. **Dangerous actions require recent re-authentication** with a passkey or TOTP.
- Email codes: 10-minute expiry, single use, limited attempts, stored hashed. Rate limits and Cloudflare Turnstile protect the send endpoint.
- **XSS:** console output, file contents, and egg metadata are untrusted and always escaped. xterm.js renders console output, never `innerHTML`.
- Postgres row-level security by `org_id`, in addition to application checks.
- Rate limiting on auth, join-token creation, subdomain creation, and bundle uploads.
- Scoped API keys (later).
- Full audit log for every mutating action.

### Staff and support
- Separate staff accounts with **hardware-key MFA**.
- Support access requires **owner approval signed with the owner's passkey** and verified by Wings (so a compromised Panel or staff account can't grant itself access), is scoped (level 1/2/3), time-limited, revocable, visible, and audited per staff member. Nodes can disable it entirely.
- **No shell access for staff.**
- An internal audit log of staff actions that staff cannot modify.

### Backups
- Encrypted on the node before upload (Kopia).
- Default key: per-node, stored **encrypted** in the Panel, so backups survive a dead box. Raptor's object storage alone can't read them.
- Optional owner-held key mode.

### Public repository
- There is no security through obscurity. Assume attackers read every line.
- No secrets in the repo, enforced by CI.
- Disclosure process in [SECURITY.md](../SECURITY.md).

## Privacy and legal
- Console logs and files can contain player data (names, IPs, chat). Raptor processes them on the owner's behalf. The ToS, privacy policy, and a DPA cover this, including support access.
- Deleting an org wipes its mirror, grants, and hosted backups (after the retention notice) and tells its nodes to unlink.
