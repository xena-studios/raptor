# Egg Compatibility

Raptor runs **Pterodactyl and Pelican eggs natively**. Eggs are Raptor's template format. They are not converted into something else, and existing egg repositories work unchanged.

## The compatibility target

**Compatibility means behaving like Pterodactyl's Wings at runtime, not just parsing egg JSON.** Egg Docker images (the "yolks") and install scripts expect a specific environment. Where docs and Pterodactyl's Wings disagree, **Pterodactyl Wings' actual behavior is the spec.**

## Formats

| Format | Source | Detection |
|---|---|---|
| `PTDL_v1` | Pterodactyl (legacy) | `meta.version` |
| `PTDL_v2` | Pterodactyl | `meta.version` |
| `PLCN_v1`–`PLCN_v3` | Pelican (JSON or YAML) | `meta.version` |

Unknown versions are rejected with a clear error rather than half-supported.

Format differences the parser normalizes (implemented in `internal/eggs`):
- **Encoding:** Pterodactyl eggs are JSON; Pelican exports YAML. Both are parsed into one model with key order preserved (the first image and first startup command are the defaults). JSON is decoded with a real JSON decoder, because some valid JSON (the `\/` escape) isn't valid YAML.
- **Images:** `PTDL_v1` has an `images` list or a single `image` (Pterodactyl's own source-engine eggs use the list); later formats have an ordered `docker_images` map (some exports use a plain list, which is accepted too).
- **Startup:** Pterodactyl has one `startup` string; Pelican has an ordered `startup_commands` map.
- **Config:** Pterodactyl stores `config.files`, `config.startup`, and `config.logs` as **JSON-encoded strings**; Pelican stores them as objects.
- **Placeholders:** Pterodactyl config files use `{{server.build.default.port}}` and `{{server.build.env.X}}`, Pelican uses `{{server.allocations.default.port}}` and `{{server.environment.X}}`. All are supported (see [Placeholders](#placeholders)).
- **Variables:** `rules` is a `|`-separated string (Pterodactyl) or a list (Pelican). Pipes inside `regex:` rules are kept together, and empty rules (`required|string|`) are dropped. `user_viewable`/`user_editable` are booleans, or `0`/`1` in `PTDL_v1`.
- **Conditional replacements:** a `find` value can be an object, `{"<if_value>": "<value>"}`. Each entry becomes its own replacement with a condition, in egg order, exactly as Pterodactyl's Panel flattens them.

The parser, validator, and config parsers were checked against **606 eggs**: Pterodactyl's built-in eggs and the Pelican community repositories (`minecraft`, `games-steamcmd`, `games-standalone`, `chatbots`, `generic`, `software`, `voice`, `database`). All 606 parse, every `regex:` rule translates to Go, and all 393 config files they define edit without errors and are unchanged by a second edit (as happens on every start).

## What must be implemented

### Images
- Multiple images per egg (`docker_images` map, e.g. Java 8/17/21), selectable per server.
- Arch awareness: before an install, Wings asks the registry for the image's manifest list (without pulling it) and refuses images with no variant for the node's CPU, and eggs whose `x-raptor.arch` excludes it. An x86-only egg like Rust fails in a second on an ARM box with a clear error, instead of after a 10 GB download with `exec format error`. If the registry can't be reached, a local copy of the image decides; if there's none, the pull does.

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
- Rules in Wings' nftables table allow **outbound internet only**. Traffic to the host itself, private ranges (RFC 1918, `100.64.0.0/10`, IPv6 ULA), link-local addresses (including the **cloud metadata endpoint `169.254.169.254`**), and the `raptor_nw` server network is dropped. See [WINGS.md](WINGS.md#firewall).
- DNS works through Docker's embedded resolver. Port 53 to the host's own upstream resolvers is allowed even when they're on a private range (e.g. a home router).
- An owner can allowlist specific private CIDRs per node with `docker.install_allow` (e.g. a local package mirror).
- `task e2e:runtime` checks all of this against a real Docker: the install container reaches the internet but not the host, a published server port, a server container, or the metadata endpoint, while a plain Docker container on the same box reaches them.

**After the install**
- Wings hands ownership of the server's files to the runtime container's UID/GID, as Pterodactyl does. The walk goes through `os.Root` and uses `lchown`: it **never follows symlinks**, never leaves the server directory, and never crosses mount points. A malicious install can't trick Wings into changing ownership of host files.
- Symlinks the script created are left as they are. They're harmless: the runtime container only sees its own directory, and every Wings file operation goes through `os.Root`.
- Output (stdout/stderr) is kept as the job's log, capped at 10 MB (the tail is kept).

**Order of checks** (`internal/wings/install`): variables are validated, then the egg's and both images' architectures are checked, then the install container runs. Nothing is downloaded for an install that can't work.

**Failure and reinstall**
- **What counts as failed:** a Docker error (image pull, container create/start) or the timeout. Like Pterodactyl, the script's **exit code does not decide success**: many community scripts end with a harmless failing command. A non-zero exit code is recorded and shown as a warning with the install log.
- A failed install marks the server `install_failed`. Files are **kept** for debugging, and the server can't start until an install succeeds, unless the owner chooses **skip install script** (Pterodactyl has the same option).
- **Reinstall** runs the script over the existing files by default (Pterodactyl behavior). The owner can instead choose **wipe and reinstall**, which always takes a safety backup first (arrives with backups in Phase 2).
- File ownership is fixed after a failed install too, so the owner can inspect or repair the files over SFTP.

### Runtime environment

Taken from Pterodactyl Wings' source (`environment/docker/container.go`, `server/server.go`) and verified by running real eggs (the [conformance suite](#conformance-test-suite)).

**Container**
| Setting | Value |
|---|---|
| Mount | Server directory → **`/home/container`** (read-write). The yolks' entrypoints `cd` there. |
| User | The host's `raptor` system user's UID:GID (Pterodactyl uses its `pterodactyl` user the same way; the yolks' built-in `container` user is overridden) |
| Hostname | The server ID |
| TTY / stdin | TTY on, stdin open (console commands are written to stdin) |
| Root filesystem | **Read-only** |
| `/tmp` | tmpfs, `rw,exec,nosuid,size=100M` |
| Capabilities dropped | `SETPCAP`, `MKNOD`, `AUDIT_WRITE`, `NET_RAW`, `DAC_OVERRIDE`, `FOWNER`, `FSETID`, `NET_BIND_SERVICE`, `SYS_CHROOT`, `SETFCAP` |
| Security options | `no-new-privileges` |
| Memory | Limit = allocation × overhead (**+15%** up to 2 GiB, **+10%** up to 4 GiB, **+5%** above), reservation = allocation, no swap by default |
| PIDs | 512 |
| Restart policy | `no` (Wings restarts servers; see [SERVERS.md](SERVERS.md)) |
| Logs | `local` driver, 3 × 20 MB |

**Environment variables, in this order**
| Variable | Value |
|---|---|
| `TZ` | Node timezone |
| `STARTUP` | The startup command **unexpanded**, with `{{VAR}}` placeholders intact |
| `SERVER_MEMORY` | Memory allocation in MiB (without the overhead) |
| `SERVER_IP` | Primary allocation IP, as allocated. For a `127.0.0.1` allocation the *port binding* uses the `raptor0` gateway instead, as Pterodactyl does with its bridge; `SERVER_IP` itself isn't changed. |
| `SERVER_PORT` | Primary allocation port |
| `P_SERVER_UUID` | Server ID |
| `P_SERVER_LOCATION` | Node name |
| `P_SERVER_ALLOCATION_LIMIT` | Allowed number of allocations |
| egg variables | Every egg variable, name upper-cased. An egg variable can't override a built-in above. |

**Startup:** the yolks' entrypoint (`/entrypoint.sh` under `tini`) converts `{{VAR}}` in `STARTUP` to `${VAR}` and `eval`s it **inside the container**. Wings never expands or runs the startup command itself, and never on the host.

### Startup, running, and stop
- `startup` command with variable substitution. **Never executed through a host shell.**
- `config.startup.done`: string or list of strings. A console line **containing** one marks the server as running; a value prefixed with `regex:` is a regular expression instead. `strip_ansi` removes color codes before matching. Eggs with no done strings are running as soon as they start.
- `config.stop`, as Pterodactyl interprets it: a value **not** starting with `^` is a console command (e.g. `stop`). A value starting with `^` is a signal: after removing one `^`, `C` or `SIGINT` → SIGINT, `SIGTERM` → SIGTERM, `SIGABRT` → SIGABRT, **anything else → SIGKILL** (so `^^C` is SIGKILL). An empty value uses Docker's normal stop. After the stop timeout, the container is killed.

### Config file parsers (`config.files`)
Applied by Wings before every start (`internal/eggs/configfile`). Pterodactyl's Wings re-serializes whole files, losing YAML comments and key order and collapsing duplicate INI keys. **Raptor's parsers edit only what they change**, so a file an owner has hand-edited keeps everything else.

| Parser | Key syntax | Behavior |
|---|---|---|
| `properties` | property name | Java `.properties` syntax (separators `=`, `:`, whitespace; `#`/`!` comments; `\` line continuations; `\uXXXX` escapes, since Java reads these files as ISO-8859-1). Matching entries are rewritten in place, keeping the key's spelling and separator; missing keys are appended. |
| `file` | line prefix | Every line starting with the key is replaced by the value (which usually repeats the key: `server.port 28015`). CRLF endings are kept. |
| `ini` | `section.key` | The first dot outside brackets splits section and key; brackets are dropped; later dots belong to the key (`[/Script/Engine.GameSession].MaxPlayers`, `[SystemSettings].net.AllowEncryption`). No dot: the section-less part at the top. Every matching key is rewritten in place (keeping spacing and quotes); missing keys go at the end of their section, missing sections at the end of the file. |
| `json` | dotted path | Key order and number formatting are kept; indentation is detected. |
| `yaml` / `yml` | dotted path | Comments, key order, and anchors are kept; indentation is detected. Multi-document files are refused rather than truncated. |
| `xml` | element path | Dots or slashes separate elements from the root (`MyConfigDedicated.SessionSettings.MaxPlayers`). Segments can be `*` or have a predicate: `[@attr='v']`, `[@attr]`, `[2]`. A value `[attr='v']` sets an attribute (7 Days to Die's `property[@name='ServerPort']`). Comments, declarations, and namespaces are kept; new elements are indented like their siblings. |

Path syntax for `json`/`yaml`: `a.b.c`, `list[0].host` (or `list.0.host`), and `*` wildcards (`servers.*.address`). Missing keys are created for unconditional rules, as in Pterodactyl.

**Values:** for JSON and YAML, integers written as strings become numbers (`"{{server.build.default.port}}"` → `25565`) and booleans stay booleans, as in Pterodactyl. Other formats write text.

**Conditions** (`if_value`): an exact value, or `regex:<pattern>` to replace only the matched part (Go regex syntax, with `$1` references in the value). Conditional rules never create missing keys.

**Where Raptor deliberately differs from Pterodactyl's Wings** (each case is one where Pterodactyl does nothing or breaks the file):
- `list[0].host` sets the element's key; Pterodactyl creates a key literally named `list[0]` (this affects the BungeeCord egg's `listeners[0].host`).
- An exact `if_value` compares against the value at the path; Pterodactyl compares against the whole document, so exact conditions never match (the BungeeCord egg's `127.0.0.1` → bridge rewrite).
- `if_value` also works for `properties`, `ini`, and `xml`, where Pterodactyl ignores it.
- More than one `*` in a path works.

**Safety**
- Files are only touched through an `os.Root` on the server directory: `..`, absolute paths, and symlinks can't reach anything outside it (fuzz-tested). Symlinks inside the directory are written through, not replaced.
- Writes are atomic (temp file + rename), keeping the file's mode and owner. New files and directories belong to the server's user.
- A file that doesn't parse, or is larger than 16 MiB, is **left untouched** and reported; the other files are still edited, and the server still starts (as in Pterodactyl).
- Output is always valid for its format: the fuzzer checks that every edit re-parses, and XML element/attribute names and characters are validated before they're written.

### Placeholders
Resolved by Wings (Pterodactyl splits this between its Panel and Wings):

| Placeholder | Value |
|---|---|
| `{{server.build.default.port}}`, `{{server.allocations.default.port}}` | Primary allocation port |
| `{{server.build.default.ip}}`, `{{server.allocations.default.ip}}` | Primary allocation IP |
| `{{server.build.env.X}}`, `{{server.environment.X}}`, `{{env.X}}` | Variable `X` |
| `{{server.build.memory}}` / `memory_limit`, `swap`, `disk` / `disk_space`, `cpu` / `cpu_limit` | Limits |
| `{{server.uuid}}` | Server ID |
| `{{config.docker.interface}}` | The `raptor0` gateway (Pterodactyl: its bridge IP) |

Unknown `server.*`/`env.*` placeholders become empty and unknown `config.*` placeholders are left as they are, as in Pterodactyl. Resolution is a single pass: a variable whose value contains a placeholder is never expanded again.

### Variables
- `env_variable`, `default_value`, `user_viewable`, `user_editable`, `rules`.
- Values are validated **before** they're used anywhere (environment, config files, install). Missing values take the egg's default.
- Values are escaped per destination: env vars (no shell involved), each config parser's format.

`rules` are Laravel validation rules, because that's what Pterodactyl and Pelican use, and eggs depend on Laravel's exact semantics:
- **Empty fails only `required` and `filled`.** A value that's empty after trimming skips every other rule. That's how Pelican validates: an empty field is validated as its default (an empty string), and Laravel only runs implicit rules on empty strings. Pterodactyl turns empty fields into null first, and null fails type rules like `string`, so Pterodactyl rejects some eggs' own defaults (e.g. SpongeVanilla's `FORGE_VERSION`, rule `string`, default empty). Pelican's behavior accepts everything Pterodactyl's does, and more.
- **Sizes depend on type.** `min`, `max`, `between`, `size`, `gt`/`gte`/`lt`/`lte` compare the number when the variable also has `numeric` or `integer` and the value is numeric; otherwise they count characters.
- **`boolean`** accepts only `1` and `0` for string values (Laravel rejects the strings `true`/`false`; eggs that want those use `in:true,false`).
- **`regex:`** patterns are PHP (PCRE) with delimiters, including bracket delimiters (`regex:([a-z]+$)`) and modifiers `i m s U A u D`. They're translated to Go.
- **Supported:** `required`, `nullable`, `sometimes`, `present`, `filled`, `string`, `integer` (and `int`), `numeric`, `boolean`, `in`, `not_in`, `min`, `max`, `between`, `size`, `gt`, `gte`, `lt`, `lte`, `digits`, `digits_between`, `regex`, `not_regex`, `alpha`, `alpha_num`, `alpha_dash`, `url`, `ip`, `ipv4`, `ipv6`, `starts_with`, `ends_with`, `lowercase`, `uppercase`, `json`, `uuid`. That covers every rule in the 606 surveyed eggs.
- Parameters are trimmed (`in: a,b` is common in eggs) and may be quoted (`in:"a,b",c`), as Laravel's parser allows.
- Unknown rules and PCRE-only patterns (lookarounds, backreferences) are **skipped, not failed**, so an egg never becomes unusable; `Egg.Lint` reports them for the catalog.

Checked against reality: of the 606 eggs, the defaults that fail their own rules are empty values for `required` fields (tokens and passwords the owner must enter) and 10 checks in 8 eggs that are genuine egg bugs Laravel rejects too (a default of `moon` for `in:Moon,Mars`, placeholder text in a `digits_between` field, a port with `integer|max:8`).

### Features
`eula`, `java_version`, `pid_limit`, `steam_disk_space`, `gsl_token` and similar. These drive Panel UI prompts (e.g. "accept the Minecraft EULA") and Wings behavior where applicable. Unknown features are ignored.

`java_version` matters in practice: the e2e tests found that the Pterodactyl-format Paper egg defaults to a Java 21 image, while current Minecraft needs Java 25. Pterodactyl detects the "requires Java" console message and prompts the user to switch images; Raptor must do the same.

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

`x-raptor` is validated when the egg is parsed: `install.timeout` must be a positive duration (`90m`, `3h`), and `arch` entries must be `amd64` or `arm64` (`x86_64` and `aarch64` are accepted as aliases). An invalid block rejects the egg rather than being half-applied.

## Egg sources

- Built-in catalog: certified eggs plus curated imports from the Pterodactyl and Pelican community repositories, with attribution and license preserved ([below](#built-in-catalog)).
- Users can import eggs from a URL or file. The Panel shows the **image registry and install script** before importing. A malicious egg is effectively a malicious program on the node.

## Built-in catalog

`eggs/` in the repository, exposed by a Go package that embeds it (`github.com/xena-studios/raptor/eggs`), for the Panel's egg catalog and the conformance suite. One directory per egg, e.g. `eggs/minecraft/paper/`:
- The egg file, an **unmodified copy** of upstream at a pinned commit, under its upstream file name. Updating an egg means replacing the file and the commit.
- `raptor.yaml`: where it came from (repository, full commit hash, path), its license, whether it's certified, which CPU architectures the game runs on, and how the conformance suite tests it:

```yaml
source:
  repo: pelican-eggs/minecraft
  commit: 75bf05db3c6c305e0fa6eef1d38c7e7176121de9
  path: java/paper/egg-paper.yaml
license: MIT
certified: true
arch: [amd64, arm64]
test:
  tier: fast                # fast | slow | manual (manual needs a reason)
  image: ""                 # the user's image choice, if not the egg's default
  memory_mib: 2048
  eula: true                # write eula.txt, as accepting the EULA in the Panel does
  variables: {}             # checked against the egg's own rules
  files: {}                 # written after the install, like a user's uploads
  done_timeout: 5m
  ready: ""                 # a console line to wait for first, if done comes early
  command: "list"           # sent to the console...
  expect: "There are 0 of a max of \\d+ players"   # ...and a reply must match
  config: {}                # file → text the egg's config parsers must write ({{port}})
```

A unit test loads every entry and checks it: the egg parses, the source matches the file, test variables pass the egg's rules, and a chosen image is one the egg offers.

## Conformance test suite

`internal/wings/conformance` runs catalog eggs through the full Wings lifecycle with the real server manager, Docker, and the eggs' unmodified images:

**install → EULA and uploads → start → done string → console command → Wings restart → stop → reinstall → start → stop → delete**

- **Wings restart:** the manager is closed and a new one started on the same state, as a Wings restart does. The server must keep running (same container start time), its console history must be refilled, and commands must still work.
- **Stop** must be the egg's own stop, not a kill: Wings reports a stop that hit its timeout (`[raptor] didn't stop within 1m0s; killed`), and the suite fails on it.
- **Reinstall** runs the install script over the existing files, then the server must start again.
- Each step is timed; a Markdown summary goes to `RAPTOR_CONFORMANCE_REPORT` (the job summary in CI).
- Failures are never retried away. On a failed install or start, the install log and console are printed. Known upstream flakiness: NeoForge's install fails occasionally (its script fetches version lists and the installer downloads many libraries without retries); its test pins exact versions, which removed most of it.

**Tiers** (`test.tier`):
| Tier | Eggs | When |
|---|---|---|
| fast | Certified: Minecraft family, Node.js, Python, Mumble, TeamSpeak, Terraria. Community: Minecraft variants, standalone games, databases, software | PRs that touch eggs or the code that runs them, nightly, on demand; amd64 and arm64 runners |
| slow | Certified: Rust, Valheim, Palworld, 7 Days to Die. Community: SteamCMD and Wine/Proton games (split across 4 jobs) | Weekly and on demand; amd64 (the games are x86-only) |
| manual | CS2 (disk, Steam token); Don't Starve Together, Eco, TF2, Unturned (account tokens) | By hand |

```bash
task e2e:conformance                         # fast tier in the VM
task e2e:conformance TIER=slow RUN=steam/rust
```

Still to come in Phase 1.8: **behavioral diff tests** (for a set of eggs, run the same server under Pterodactyl Wings and Raptor Wings, and compare the environment, files written by config parsers, and startup command); and a symlink and path-traversal suite.

Fuzz tests (`go test -fuzz`) cover egg parsing, rule validation, PHP regex translation, placeholder resolution, every config parser (output must always re-parse), `.properties` round trips, and config file paths (nothing outside the server directory is ever touched). Inputs the fuzzer found are kept in `testdata/fuzz` and run as regular tests.

## Community eggs

47 community eggs are in the catalog (`certified: false`) beside the certified ones, tested the same way. Their CI jobs report failures in the job summary without failing the build: an upstream egg breaking shouldn't block Raptor's PRs, but it should be seen.

**How they were chosen.** Nobody publishes egg download counts, so the list was built from the maintained [pelican-eggs](https://github.com/pelican-eggs) repositories (317 eggs; Raptor's parser reads 316, the other is an empty file upstream):
- Games with large player bases that people self-host, plus a few widely used non-game servers (databases, Lavalink, Forgejo). Recent upstream commits were a tiebreaker.
- Anonymous downloads only: eggs that need a game account to install (DayZ, Starbound, Assetto Corsa's Steam variant) were left out. Eggs that need an account token only to run (Don't Starve Together, Eco, TF2, Unturned) are in the **manual** tier.
- Windows-only servers run under Wine or Proton are included where the egg does it (Enshrouded, V Rising, Abiotic Factor, Conan Exiles); they're x86-only.
- The upstream arm64 variants of Bedrock and Factorio (x86 servers run through box64) are included for arm64 coverage.

**An egg that doesn't work isn't in the catalog.** The first runs found these broken (as of the pinned commits); they were dropped, and are worth reporting or fixing upstream:

| Egg | What's broken |
|---|---|
| Folia, Waterfall | Use PaperMC's v2 download API, which no longer serves them |
| Mohist | Mohist's API returns no build, so nothing is downloaded |
| NanoLimbo | Its GitHub release lookup returns an empty download URL |
| PocketMine-MP | Install fails on both architectures (the arm64 PHP build script 404s) |
| Uptime Kuma | Its done string no longer matches the app's output, so it never reaches running |
| Luanti (Minetest) | The image no longer has the `minetest` binary after the rename to Luanti |
| Mindustry | The server needs Java 17; the egg only offers a Java 11 image |
| Redis 7 | Starts Redis with `--daemonize yes`, which sends the log (and the done string) to /dev/null |
| Left 4 Dead 2 | SteamCMD rejects the Linux download ("Invalid platform") |
| Necesse | The install doesn't fetch the bundled Java runtime the startup uses |
| Space Engineers | The default world template is missing ("Premade world not found") |
| TeamSpeak 6 | The download fails, so there's no server binary |
| Spigot | The install script builds with the install image's Java 8 unless the version is "latest", and "latest" (Minecraft 26.x) needs Java 25, not the Java 21 it downloads; on arm64 it downloads an x86 JDK |
| Sons of the Forest | Crashes under Wine during startup (after the first-boot restart) |
| The Isle Evrima | Never reaches its done string (40 minutes) |
| Stationeers | Killed by the OOM killer at 4 GiB; at 8 GiB never reaches its done string |
| Risk of Rain 2 | The install doesn't produce the server executable (`Risk of Rain 2.exe` missing) |
| Pavlov VR | Not broken, but needs an API key from the developer; not added |

Eggs kept with a workaround in their test settings (each explained in its `raptor.yaml`): Forge, NeoForge, and Forgejo pin versions their scripts can install; TShock and several Minecraft eggs pick a newer image the egg offers; Palworld waits for its real ready line. Known flaky: Project Zomboid (SteamCMD's intermittent "Missing configuration" error) and NeoForge (see above).

## Certified at launch

| Category | Eggs |
|---|---|
| Minecraft | Paper, Purpur, Fabric, Forge, NeoForge, Vanilla, Velocity, BungeeCord |
| Steam | Rust (incl. Oxide/Carbon), Valheim, Palworld, CS2, Terraria, 7 Days to Die |
| Other | Discord bot (Node.js), Discord bot (Python), TeamSpeak, Mumble, generic Node.js, generic Python (the Discord bot entries are the generic Node.js and Python eggs) |

Notes:
- **Hytale isn't certified.** Its official server image signs in to a Hytale account (OAuth device flow) before it downloads the server, so it can't be tested unattended. It stays available as a community egg, and owners sign in with their own account from the console.
- Most Steam dedicated servers are **x86_64-only** (Rust, CS2, …). The Panel hides them on arm64 nodes. No x86 emulation in v1.
- Only **anonymous SteamCMD** games in v1. No storing Steam credentials.
- Minecraft: users must accept the EULA. Server jars are downloaded at install time and never redistributed.
- Windows-only servers (e.g. ARK: Survival Ascended) are not supported in v1.
