# Egg Compatibility

Raptor runs **Pterodactyl and Pelican eggs natively**. Eggs are Raptor's template format. They are not converted into something else, and existing egg repositories work unchanged.

## The compatibility target

**Compatibility means behaving like Pterodactyl's Wings at runtime, not just parsing egg JSON.** Egg Docker images (the "yolks") and install scripts expect a specific environment. Where docs and Pterodactyl's Wings disagree, **Pterodactyl Wings' actual behavior is the spec.**

## Formats

| Format | Source | Detection |
|---|---|---|
| `PTDL_v1` | Pterodactyl (legacy) | `meta.version` |
| `PTDL_v2` | Pterodactyl | `meta.version` |
| `PLCN_v*` | Pelican | `meta.version` |

Unknown versions are rejected with a clear error rather than half-supported.

## What must be implemented

### Images
- Multiple images per egg (`docker_images` map, e.g. Java 8/17/21), selectable per server.
- Arch awareness: a server can only use images with a manifest for the node's architecture (checked before install).

### Install

Install scripts are the least trusted code Raptor runs: they come from the egg, run as **root inside the container** (many use `apt-get`), and have internet access. They run in a **separate, short-lived install container**, never in the server's runtime container, and never on the host.

**Lifecycle**
- Runs as a `server.install` / `server.reinstall` job, holding the server's lock. The server must be stopped.
- The egg's `script.container` image is pulled first (with retries and an architecture check), then the container runs `script.entrypoint` on the script.
- The container is **always removed** afterwards, on success, failure, or timeout.

**What the container can see**
| Mount | Access | Contents |
|---|---|---|
| `/mnt/server` | read-write | The server's directory (on the quota volume, so the disk limit applies during install) |
| `/mnt/install` | **read-only** | `install.sh`: the egg's script, line endings normalized to LF (as Pterodactyl does), written to a per-job temp directory that is deleted afterwards |

Nothing else from the host is mounted: no Docker socket, no host paths, no other servers.

**Environment:** the egg variables (validated against their rules first) plus the same built-ins as the runtime (`SERVER_MEMORY`, `SERVER_IP`, `SERVER_PORT`, …).

**Hardening**
- Root inside the container (required for egg compatibility), but **never `--privileged`**, `no-new-privileges`, Docker's default seccomp profile, and Docker's default capabilities minus `NET_RAW`, `MKNOD`, `AUDIT_WRITE`, and `SETFCAP`. The exact set is confirmed by the conformance suite.
- `/tmp` is a tmpfs.
- **Limits:** memory = the larger of the server's memory and 1 GiB; PID limit 512; low CPU and I/O weight so installs can't lag running servers; at most 2 concurrent installs per node.
- **Timeout:** 2 hours by default (SteamCMD downloads can be very large), overridable per egg with `x-raptor.install.timeout`. On timeout the container is killed and the install is marked failed.

**Network isolation**
- Install containers use their own Docker network, `raptor_install`, with inter-container traffic disabled.
- Rules in the `RAPTOR` chain allow **outbound internet only**. Traffic to the host itself, private ranges (RFC 1918, `100.64.0.0/10`, IPv6 ULA), link-local addresses (including the **cloud metadata endpoint `169.254.169.254`**), and the `raptor_nw` server network is dropped.
- DNS works through Docker's embedded resolver.
- An owner can allowlist specific private CIDRs per node (e.g. a local package mirror).

**After the install**
- Wings hands ownership of the server's files to the runtime container's UID/GID, as Pterodactyl does. The walk goes through `os.Root` and uses `lchown`: it **never follows symlinks**, never leaves the server directory, and never crosses mount points. A malicious install can't trick Wings into changing ownership of host files.
- Symlinks the script created are left as they are. They're harmless: the runtime container only sees its own directory, and every Wings file operation goes through `os.Root`.
- Output (stdout/stderr) is kept as the job's log, capped at 10 MB (the tail is kept).

**Failure and reinstall**
- A failed install marks the server `install_failed`. Files are **kept** for debugging, and the server can't start until an install succeeds, unless the owner chooses **skip install script** (Pterodactyl has the same option).
- **Reinstall** runs the script over the existing files by default (Pterodactyl behavior). The owner can instead choose **wipe and reinstall**, which always takes a safety backup first.

### Runtime environment
- Server files mounted at **`/home/container`**, the working directory.
- Container user: Pterodactyl-compatible UID/GID.
- Environment: every egg variable, plus built-ins such as `STARTUP`, `SERVER_MEMORY`, `SERVER_IP`, `SERVER_PORT`, `P_SERVER_UUID`, `P_SERVER_LOCATION`, `P_SERVER_ALLOCATION_LIMIT`, `TZ`. The exact list and values are taken from Pterodactyl Wings.
- The yolks' entrypoints perform their own `{{VAR}}` → `${VAR}` substitution on `STARTUP`. Wings passes `STARTUP` exactly as Pterodactyl Wings does.

### Startup, running, and stop
- `startup` command with variable substitution. **Never executed through a host shell.**
- `config.startup.done`: string or list of strings. Seeing one in console output marks the server as running.
- `config.stop`: a console command (e.g. `stop`), or a signal: `^C` (SIGINT), `^^C` (SIGKILL), `^X`-style values as Pterodactyl handles them. Timeout, then kill.

### Config file parsers (`config.files`)
Parsers: `properties`, `yaml`, `json`, `ini`, `xml`, `file` (line-based find/replace).
- Wildcard keys and nested paths as Pterodactyl supports them.
- Placeholders: `{{server.build.default.port}}`, `{{server.build.memory}}`, `{{server.build.env.VAR}}`, `{{config.docker.interface}}`, etc.
- **Security:** config files are written **by Wings on the host**. Every path is resolved through `os.Root` confined to the server directory. `..`, absolute paths, and symlinks that escape are rejected.

### Variables
- `env_variable`, `default_value`, `user_viewable`, `user_editable`, `rules`.
- `rules` are Laravel-style (`required|string|max:20|in:a,b|regex:/…/`). The subset used by real eggs is reimplemented in Go, **and validation always happens before substitution.**
- Values are escaped per destination: env vars (no shell involved), each config parser's format.

### Features
`eula`, `java_version`, `pid_limit`, `steam_disk_space`, `gsl_token` and similar. These drive Panel UI prompts (e.g. "accept the Minecraft EULA") and Wings behavior where applicable. Unknown features are ignored.

### Inheritance
`config.extends` / `copy_script_from` (Pterodactyl legacy). Resolved when importing the egg.

## Raptor extensions

Raptor-specific data lives under a namespaced key that Pterodactyl and Pelican ignore, so **Raptor-enhanced eggs still load elsewhere**:

```json
"x-raptor": {
  "arch": ["amd64", "arm64"],
  "backup": {
    "pre":  ["save-off", "save-all flush"],
    "post": ["save-on"],
    "wait_for": "Saved the game"
  },
  "health": { "type": "port", "protocol": "tcp" },
  "install": { "timeout": "3h" },
  "players": { "query": "minecraft" },
  "certified": true
}
```

## Egg sources

- Built-in catalog: certified eggs plus curated imports from the Pterodactyl and Pelican community repositories, with attribution and license preserved.
- Users can import eggs from a URL or file. The Panel shows the **image registry and install script** before importing. A malicious egg is effectively a malicious program on the node.

## Conformance test suite

Built early (during Wings core) and run in CI:
- The top ~50 community eggs plus every certified egg.
- For each: import → install → start → "done" detected → console command → stop → reinstall.
- **Behavioral diff tests:** for a set of eggs, run the same server under Pterodactyl Wings and Raptor Wings, and compare the environment, files written by config parsers, and startup command.
- Fuzz tests for variable substitution and config parsers.

## Certified at launch

| Category | Eggs |
|---|---|
| Minecraft | Paper, Purpur, Fabric, Forge, NeoForge, Vanilla, Velocity, BungeeCord |
| Hytale | Hytale dedicated server |
| Steam | Rust (incl. Oxide/Carbon), Valheim, Palworld, CS2, Terraria, 7 Days to Die |
| Other | Discord bot (Node.js), Discord bot (Python), TeamSpeak, Mumble, generic Node.js, generic Python |

Notes:
- Most Steam dedicated servers are **x86_64-only** (Rust, CS2, …). The Panel hides them on arm64 nodes. No x86 emulation in v1.
- Only **anonymous SteamCMD** games in v1. No storing Steam credentials.
- Minecraft: users must accept the EULA. Server jars are downloaded at install time and never redistributed.
- Windows-only servers (e.g. ARK: Survival Ascended) are not supported in v1.
