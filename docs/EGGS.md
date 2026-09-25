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
- The install script runs in a **separate install container** using the egg's `script.container` image and `script.entrypoint`.
- The server directory is mounted at `/mnt/server`. Nothing else from the host.
- Install environment variables include the egg variables plus the built-ins.
- Install containers run with network access and are removed afterwards. Install logs are kept as job logs.

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
