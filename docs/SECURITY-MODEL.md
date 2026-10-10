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
6. The Panel's signing key (proves the Panel to nodes and signs per-user command grants), and its data key (encrypts TOTP secrets)
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

### Server files

A server's directory is written by code Raptor doesn't trust: the egg's install script (root in the install container) and the game itself. Anything can be planted there: symlinks to host files or other servers, directory symlinks, loops, FIFOs, hard links, setuid and unreadable files. Wings touches those files when it fixes ownership after an install, edits config files before every start, tags files for the disk quota, measures disk usage, and deletes a server. None of these may reach outside the directory or hang:

- **Config file edits** go through `os.Root`: symlinks and paths that escape are refused and reported on the console (the server still starts); only regular files are edited, so a FIFO can't block a start; a symlink that stays inside is written through. Egg paths with `..` or a leading `/` land inside the directory.
- **Ownership fix** walks through `os.Root` with `Lchown`, never following a symlink.
- **Quota tagging** opens files with `O_NOFOLLOW` through `os.Root` and skips symlinks; **disk usage** never follows symlinks.
- **Delete** removes links, not their targets.
- Edits happen only while the server is stopped, so nothing can swap a file between Wings checking it and writing it.
- **Users' file access** (the web file manager and SFTP) goes through one `os.Root` layer: only regular files are opened (with `O_NONBLOCK`, and checked again once open, so a FIFO swapped in can't block), links are followed only inside the directory, deleting removes links, and new files belong to the servers' user with plain permission bits. **Archives** store links as links and never follow them out; extraction cleans each name, skips what would climb out and special files, drops setuid bits, replaces links in its way rather than writing through them, and is bounded by the server's disk limit. The server's own process can race these operations (swap a file for a link between a check and a use), but `os.Root` resolves every path at the moment of use, so a race can only reach another file inside the same directory.
- **The egg's `file_denylist`** is enforced by Wings for the web file manager and SFTP, so a compromised or buggy Panel can't open the files an egg protects. Links are resolved before checking, so a link can't reach a denied file under another name, and a directory holding a denied file can't be moved or deleted.

**Tested:** `TestPathSafety` (in `task e2e:runtime` and CI) uses a hostile egg that plants all of the above, including links to `/etc/passwd`, `/etc`, `/usr`, a root-only host file, and another server's files, then runs install, start, disk usage, SFTP, the web file manager's operations, compressing everything and extracting a hostile archive, reinstall, and delete, and checks that nothing outside the directory changed (content, mode, and owner of the files, and mode and owner of the directories the links point at), nothing hung, and the other server still starts. The suite was checked against deliberately broken Wings builds: an ownership fix that follows symlinks, config edits that resolve paths without `os.Root`, and user file access that opens files without it. It failed on all three. The config parsers' path handling is also fuzzed.

### Enrollment and identity
- Join tokens: single-use, 1-hour expiry, org-bound, stored hashed.
- **Keys are generated on the node and never leave it.** The Panel stores only the public key. The node proves itself on every connection by signing a fresh challenge (the Panel's random nonce and its own, the node ID, the connection's purpose, and a timestamp, so it can't be replayed or used for another connection).
- **The Panel proves itself** with its signing key, which Wings pins at enrollment, by signing the node's nonce the same way. Wings checks it before signing anything, so an impostor never gets a node signature. TLS terminates at Cloudflare, so the TLS certificate alone doesn't prove the Panel's identity to Wings. Each signature names its signer, so one side's can't pass as the other's, and handshake signatures can't pass as command grants (different fields).
- Node removal revokes the node's key and drops its connection immediately.
- SFTP host keys are generated on the node; the fingerprint is reported to the Panel over the authenticated connection and shown to users.
- **SFTP** is off by default. When on, it serves only the `sftp` subsystem (no shell, commands, or forwarding), refuses FIFOs and device nodes, never sets setuid bits or ownership, limits failed logins per address, and ends sessions when an install or a backup restore starts. Cached keys stop working once the Panel rejects them or hasn't confirmed them in 30 days. Temporary passwords (per user and server, random, 24 characters, stored hashed, from 1 hour to 30 days) are checked by the Panel on every login and never cached; sessions end when they run out or are revoked. See [WINGS.md](WINGS.md#files-and-sftp).
- **The Panel's signing key** is kept separate from the Panel's application secrets, ideally in an HSM or KMS later, at minimum a separate encrypted secret with restricted access.
- **The Panel's data key** (`PANEL_DATA_KEY`) is a separate file from the signing key, so either can be rotated without the other. A database leak without it doesn't reveal TOTP secrets. Losing it turns off every account's authenticator app (recovery codes and passkeys still work, and decrypting fails closed), so it's backed up with the same care as the signing key.
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
   - the origin is `https://app.raptorpanel.net` and the `rpIdHash` matches `app.raptorpanel.net`, so phishing sites and other `raptorpanel.net` subdomains can't produce valid signatures;
   - both user presence and user verification flags are set;
   - the command hasn't expired;
   - its `command_id` has never run (the `executed_commands` table);
   - the signature counter moved forward, when the authenticator keeps one.

   Anything missing or invalid is rejected and logged, even though it came from the Panel. Read-only commands (listing, reading, and starting a download of files) skip the `executed_commands` record: running one twice changes nothing, and their results (file contents) shouldn't be stored.

Verification takes about 0.1 ms on the node. The user's cost is one fingerprint or PIN tap, which replaces the re-authentication prompt these actions already needed. Bulk actions sign once (one signature over the list of servers), and the browser signs before sending, so there's no extra round trip.

**Which actions are signed** (anything that destroys data, changes what code runs, or changes who has access):
- **Creating a server** (it chooses an egg, and so the install script and image that run)
- Deleting a server; reinstalling with "wipe"; restoring a backup over current files. Restoring a **deleted** server's backup onto another server needs an **owner's** key: a delegation for the new server isn't enough, since the delegate may never have had access to the old server's files.
- Deleting a backup, unlocking one, or lowering backup retention (each deletes backups, directly or at the next retention run)
- Adding, changing, or deleting a backup destination, and sending a server's backups to a destination it didn't use (each decides where servers' files can be copied; a folder destination is also written as root on the node). Backups are encrypted with the node's key, but a compromised Panel mustn't be able to quietly point them at its own bucket. Raptor Backup Storage is the exception, unsigned because it can only be Raptor's own bucket (its name is pinned in Wings and unique across Backblaze) and the node's own folder in it.
- Changing the egg, install script, startup command, or Docker image (Wings compares the update with its own records to decide; the Panel can't mislabel it)
- Granting support access
- Adding SSH/SFTP keys or sub-users, and delegating signed actions to them
- Changing the node's trusted keys
- Removing the node

**Not signed** (to keep everyday use fast): start/stop/restart/kill, console commands, a plain reinstall (it re-runs the egg the owner already approved), file browsing and editing, variables, schedules, and settings that don't change code or access. These still need the Panel's per-user grant. A compromised Panel could read files and the console and disrupt servers, but not wipe them beyond recovery, backdoor them through eggs or images, or quietly add access. It could still change or delete files through the unsigned file manager, including adding a plugin that runs code in the game: signing every edit would make the file manager unusable. What it can't do is make that stick: deleting, unlocking, or thinning out backups is signed, so the files can be restored from backups (on by default), and a node's own record of commands shows what it did.

**Trusted keys: rooted on the node, not in the Panel**

If the Panel simply told Wings which keys to trust, a compromised Panel would send its own. So the set of trusted keys lives in Wings' SQLite and only changes by rules Wings enforces:
- **At enrollment** the owner signs the enrollment (including the join token) with their passkey, and that key is pinned on the node. `raptor bootstrap` prints the key's fingerprint and the browser shows the same fingerprint; owners are told to compare them. This is trust-on-first-use: a Panel that was already compromised at the moment of enrollment could substitute a key, and comparing fingerprints catches that.
- **Adding or removing a trusted key** (a new device, another org owner) must be signed by a key the node already trusts.
- **Sub-users** get dangerous actions only through a **delegation signed by an owner's passkey**: "user X's key may reinstall server S". The Panel can't create one. Delegations can expire and be revoked (also signed). The member's public key reaches the owner's browser through the Panel, so a Panel that was compromised could offer its own key instead: the owner's screen shows the key's fingerprint, and the member's Security page shows their passkeys' fingerprints, for the two to compare before the owner signs.
- **Recovery without the Panel:** if every trusted passkey is lost, root on the box runs `raptor keys reset`. It prints a one-time pairing code (10 characters, 15 minutes, cancelled after 5 wrong tries); the owner enters it in the Panel and signs the pairing (`keys.pair`) with a new passkey. The pairing must be signed by the key it pairs, so the owner proves they hold it. **Root then confirms the key's fingerprint on the box** (the browser shows the same fingerprint) before Wings pins it as the only owner key, removing every other key and delegation. The code alone isn't enough, because the owner types it into the Panel: a compromised Panel could see it and pair its own key instead, and the fingerprint check on the box catches that. Root on the box owns the box, as the trust boundaries already say.
- `raptor keys list` shows the trusted keys and delegations on the node, with fingerprints (the first 80 bits of the SHA-256 of the COSE public key, in base32 groups).
- **The audit log:** every command that needed a signature is recorded on the node, whether it ran or was rejected (with why), with the user, the signing key's name and fingerprint, and the SHA-256 of what was signed; so are pairings and key resets. It's kept a year, apart from the week-long record of executed commands, and `raptor audit` lists it.

**The web app is part of the trust boundary.** Browsers don't show *what* a passkey is signing, so an attacker who controls the JavaScript could display "Restart" while asking for a signature on "Delete". Defenses:
- The web app is **served from separate static hosting, not the API servers**, on its own origin (`app.raptorpanel.net`). Compromising the API or the database can't change the code users run. Deploying the web app requires separate credentials.
- **The API has a different origin** (`api.raptorpanel.net`). On the app's origin, a compromised API could serve a page that asks passkeys to sign, or register a service worker that replaces the app; on its own origin it can do neither, and it can't use the app's RP ID.
- **The RP ID is `app.raptorpanel.net`**, so the landing page, docs, and status page can't ask users' passkeys for signatures even if they're compromised.
- A **strict Content Security Policy** (no inline scripts, no third-party scripts, `script-src` limited to the app's own hashed bundles, Trusted Types).
- **Reproducible builds:** each release publishes the bundle hashes, so anyone can check that the deployed app matches the public source.
- **The domain accounts are part of this boundary:** whoever controls DNS or the static host controls the app. The registrar and Cloudflare accounts use hardware-key 2FA, the domains have registrar lock, and API tokens are scoped so the Panel servers can't change DNS or the web app.
- **Alerts straight from the node:** Wings reports every signed dangerous action (including rejected attempts) to the owner through a channel configured on the node in `config.yml` (a Discord or generic webhook; see [WINGS.md](WINGS.md#notifications)), not through the Panel, and `raptor audit` lists them from the node's own records. A tampered action gets noticed even if the Panel hides it.

Even if every one of these failed, each malicious action would still need a real person's passkey at that moment, bound to one command. That turns "one Panel breach controls every node" into "an attacker must trick specific users, one action at a time".

**Implementation status:** the envelope, grant check, WebAuthn verification, trusted-key store, delegations, and key management are implemented in Wings (`internal/wings/command`, Phase 1.5). They're tested with a software authenticator producing real ES256, EdDSA, and RS256 assertions, including every attack in the flow above (changed params, moved signatures, other users' and untrusted keys, phishing origins, other RP IDs, missing user verification or presence, registration instead of assertion, cross-origin frames, forged signatures, cloned counters, delegation scope and expiry), and fuzzed. Pinning at enrollment is implemented: "Add a node" asks the owner's passkey to sign `nodecmd.OwnerPin` (the join token's hash and the passkey's public key, RFC 8785, with a purpose so it can't pass as a command), the Panel stores it with the token for the caller's own registered passkey only, and `raptor link`/`bootstrap` hand it to Wings (`LocalService.PinOwnerKey`), which checks the signature with its own `app_url`, that it names the very token used, and that no owner key is trusted yet, then prints the fingerprint for the owner to compare with the browser's. `raptor keys list|reset` and `raptor audit` are implemented (Phase 2). The web app signs (`web/src/lib/canonical.ts`, `signed.ts`): it builds the command itself (a UUIDv7 and a 5-minute expiry), hashes the same RFC 8785 form Wings does (a Go test and a browser test check one shared vector, hash included), and asks the passkey to sign with `userVerification: required`. Pairing after `raptor keys reset` happens on the node's page, and the browser computes the fingerprint to compare from the key it signs with, never from what the Panel says.

- Signature counters: authenticators that keep a counter must move it forward, or the signature is rejected as a possible cloned key. Synced passkeys always report 0 and are exempt.
- Params must not rely on integers above 2^53: canonical JSON numbers are doubles in browsers.

**Requirements**
- Dangerous actions need a **passkey**. Authenticator-app codes can't be verified by Wings, so they're not enough for these actions (they still protect sign-in). Nearly every current phone and laptop supports passkeys, and hardware security keys cover the rest.
- On by default for every node, with no Panel-side way to turn it off. Only root on the box can change it (`raptor keys` commands).

### Releases and supply chain
- Wings releases are **signed** with minisign (Ed25519): the signature covers `checksums.txt`, which covers every binary. CI only builds **draft** releases; the maintainer signs and publishes locally. The private key is kept **offline** and never stored in the repository or CI.
- The install script is generated per release with the binary's SHA-256 embedded, and verifies it before running anything. The binary verifies every later update's minisign signature with an embedded public key, and the signed trusted comment must name the release being installed, so a validly signed older release can't be replayed as a newer one. A channel never moves a node to an older version; only the owner can, explicitly.
- Dependencies pinned. CI runs `govulncheck`, `npm audit`, license checks, and secret scanning (gitleaks).
- Egg imports show the image registry and install script before import, with warnings for images from publishers the community eggs don't use and for install scripts that run a downloaded script; the admin has to say they trust the egg. The Panel fetches the link itself, https only on port 443, and refuses private, loopback, link-local (cloud metadata), CGNAT, and NAT64/6to4 addresses where it dials, after DNS, so a link can't reach the Panel's own network ([PANEL.md](PANEL.md#imported-eggs)).

### Panel
- **Passwordless auth built into the Panel** (passkeys, OAuth, email codes; TOTP 2FA). No passwords exist to leak. Passkeys are phishing-resistant and preferred. Details in [PANEL.md](PANEL.md#auth).
- OAuth logins only link to existing accounts when the provider verified the email (prevents account takeover through unverified emails).
- **Tenant isolation in the database:** user requests run under row-level security as `raptor_app`, so a handler that forgets to check org membership still can't read or change another org's rows (docs/PANEL.md#permissions).
- The Panel issues its own sessions (hashed tokens in a host-only `__Host-` cookie on `api.raptorpanel.net`: `HttpOnly`, `Secure`, `SameSite=Strict`). Other `raptorpanel.net` subdomains are treated as untrusted: the API only accepts browser requests with `Origin: https://app.raptorpanel.net`, and the `__Host-` prefix stops them from setting the session cookie. **Sensitive account changes require re-authenticating within 5 minutes** with a passkey or TOTP (an email code only on accounts with neither), and signing in by email doesn't count on accounts that have one, so access to someone's inbox isn't enough to remove their passkeys.
- Email codes: 10-minute expiry, single use, limited attempts, stored hashed. Rate limits and Cloudflare Turnstile protect the send endpoint; Turnstile runs on its own origin (`verify.raptorpanel.net`), so even Cloudflare's script can't reach the app's passkeys.
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
- Key: one random repository password per node, generated on the box and kept in its SQLite. By default a copy is stored **encrypted** in the Panel (once the node is linked), so backups survive a dead box. Raptor's object storage alone can't read them.
- S3 credentials stay on the node; events and listings never include the secret key.
- Kopia runs in a separate worker process with a memory cap, so a huge backup can't push the kernel into killing game servers or Wings (docs/WINGS.md#backups).
- Optional owner-held key mode.

### Public repository
- There is no security through obscurity. Assume attackers read every line.
- No secrets in the repo, enforced by CI.
- Disclosure process in [SECURITY.md](../SECURITY.md).

## Privacy and legal
- Console logs and files can contain player data (names, IPs, chat). Raptor processes them on the owner's behalf. The ToS, privacy policy, and a DPA cover this, including support access.
- Deleting an org wipes its mirror, grants, and hosted backups (after the retention notice) and tells its nodes to unlink.
