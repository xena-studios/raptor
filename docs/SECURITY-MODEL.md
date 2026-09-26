# Security Model

## The core risk

**The Panel can send commands to Wings, and Wings runs as root on every customer's box.** A compromise of the Panel must not become root access on every node. Most rules below exist to limit that blast radius.

## Assets

1. Customer nodes (root on their hardware)
2. Game server data (worlds, configs, player data)
3. Backups
4. User accounts and sessions
5. The Wings release signing key
6. The Panel's signing key (proves the Panel to nodes and signs per-user command grants)

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
- Support access requires **owner approval**, is scoped (level 1/2/3), time-limited, revocable, visible, and audited per staff member. Nodes can disable it entirely.
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
