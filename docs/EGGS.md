# Egg Compatibility

Raptor runs **Pterodactyl and Pelican eggs natively**. Eggs are Raptor's template format. They are not converted into something else, and existing egg repositories work unchanged.

## The compatibility target

**Compatibility means behaving like Pterodactyl's Wings at runtime, not just parsing egg JSON.** Egg Docker images (the "yolks") and install scripts expect a specific environment. Where docs and Pterodactyl's Wings disagree, **Pterodactyl Wings' actual behaviour is the spec.**

## Formats

| Format | Source | Detection |
|---|---|---|
| `PTDL_v1` | Pterodactyl (legacy) | `meta.version` |
| `PTDL_v2` | Pterodactyl | `meta.version` |
| `PLCN_v1`–`PLCN_v3` | Pelican (JSON or YAML) | `meta.version` |
| `RPTR_v1` | Raptor's own ([below](#raptor-eggs)) | `meta.version` |

Unknown versions are rejected with a clear error rather than half-supported.

Format differences the parser normalises (implemented in `internal/eggs`):
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
| `/mnt/install` | **read-only** | `install.sh`: the egg's script, line endings normalised to LF (as Pterodactyl does), written to a per-job temp directory that is deleted afterwards; `bin/curl`, Raptor's curl ([Downloads](#downloads)) |
| `/mnt/install-report` | read-write | Where that curl records downloads that failed; a per-job temp directory, deleted afterwards |

Nothing else from the host is mounted: no Docker socket, no host paths, no other servers.

**Environment:** the egg variables (validated against their rules first) plus the same built-ins as the runtime (`SERVER_MEMORY`, `SERVER_IP`, `SERVER_PORT`, …). `PATH` is the image's own with `/mnt/install/bin` in front.

#### Downloads

DECISIONS #336. Most eggs download with a bare `curl` (no `--fail`, no retries, no `set -e`), so one reset connection used to leave a truncated jar behind an install that "succeeded". `/mnt/install/bin/curl` (`internal/wings/docker/installbin/curl`, POSIX sh) sits first on PATH and:
- runs the image's own curl (the first `curl` on PATH after its own directory; none: `curl: not found`, exit 127, as without it), with `--retry 5 --retry-delay 2 --retry-max-time 300` before the script's own options (so the script's win), plus `--retry-all-errors` where the image's curl has it (7.71+; it also retries resets mid-transfer, like HTTP/2 stream errors). A curl that knows neither gets the arguments untouched. Output, `-w`, `-o` and exit codes are curl's own; curl writes `-w` once and empties an `-o` file before a retry. Two costs: output piped to another command can't be taken back, so a transfer that breaks off and is retried pipes the partial data and then all of it (without the retry, only the partial data); and `--retry-all-errors` also retries a `--fail` probe that gets a 404, so that probe takes about 10 seconds longer.
- records a run that still fails **on the network**: exit 5, 6, 7, 16, 18, 28, 35, 52, 55, 56, 60, or 92 (couldn't resolve or connect, timeout, TLS, the transfer broke off), with its URLs. HTTP errors (`--fail`'s 22, which scripts use to probe for a version), a closed pipe (23), and bad options are the script's business and aren't recorded.
- isn't used by `runuser -l`/`su -` (a login shell resets PATH), by `/usr/bin/curl` called by path, or by `wget` (GNU wget already retries 20 times; BusyBox's takes other options).

`TestCurlWrapper` runs it under sh, dash, and bash against a stand-in curl (new, pre-7.71, and one with no `--retry`); the runtime e2e test runs it in BusyBox.

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
- **What counts as failed:** a Docker error (image pull, container create/start), the timeout, or, after the script ran:
  - **a download that failed** on the network even after retries ([Downloads](#downloads)): "A download failed: <url> (curl exit N). Reinstall to try again."
  - **a broken startup jar:** if the startup command runs `-jar <file>` (a literal name or `{{VARIABLE}}`s, without shell substitutions) and that file is there but is empty or isn't a valid zip: "server.jar isn't a valid jar file (it's corrupt or not a jar), so the server can't start. Reinstall to try again." A jar that isn't there is fine: some eggs fetch it when the server first starts. It's read through `os.Root`.

  The reason ends the install log and is the server's install error. Like Pterodactyl, the script's **exit code does not decide success**: many community scripts end with a harmless failing command. A non-zero exit code is recorded and shown as a warning with the install log.
- A failed install marks the server `install_failed`. Files are **kept** for debugging, and the server can't start until an install succeeds, unless the owner chooses **skip install script** (Pterodactyl has the same option).
- **Reinstall** runs the script over the existing files by default (Pterodactyl behaviour), after a **safety backup** of them (kept 7 days, never counted by retention; none for a server with no files). The owner can instead choose **wipe and reinstall** (signed with their passkey), which takes the same safety backup, then removes every file before the script runs. If the backup fails, nothing is changed and the reinstall doesn't run. Changing a server's type is a reinstall too, either way. A first install takes none.
- File ownership is fixed after a failed install too, so the owner can inspect or repair the files over SFTP.

### Runtime environment

Taken from Pterodactyl Wings' source (`environment/docker/container.go`, `server/server.go`), verified by running real eggs (the [conformance suite](#conformance-test-suite)), and compared setting by setting against the real Pterodactyl by the [behavioural diff](#behavioral-diff-against-pterodactyl).

**Container**
| Setting | Value |
|---|---|
| Mount | Server directory → **`/home/container`** (read-write). The yolks' entrypoints `cd` there. |
| `/etc/machine-id` | Read-only, per server: the server ID without dashes (as Pterodactyl writes it; Hytale encrypts its tokens with it). Written to `/run/raptor/machine-id/<id>` before every start. |
| User | The host's `raptor` system user's UID:GID (Pterodactyl uses its `pterodactyl` user the same way; the yolks' built-in `container` user is overridden) |
| Hostname | The server ID |
| TTY / stdin | TTY on, stdin open (console commands are written to stdin) |
| Root filesystem | **Read-only** |
| `/tmp` | tmpfs, `rw,exec,nosuid,size=100M` |
| Capabilities dropped | `SETPCAP`, `MKNOD`, `AUDIT_WRITE`, `NET_RAW`, `DAC_OVERRIDE`, `FOWNER`, `FSETID`, `NET_BIND_SERVICE`, `SYS_CHROOT`, `SETFCAP` |
| Security options | `no-new-privileges` |
| Memory | Limit = allocation × overhead (**+15%** up to 2 GiB, **+10%** up to 4 GiB, **+5%** above), reservation = allocation, no swap by default |
| PIDs | 512 |
| CPU / I/O weight | Docker's defaults (CPU shares 1024); Pterodactyl sets I/O weight 500 on every server. Both are relative, so servers share the same way. |
| DNS | The host's resolvers through Docker. Pterodactyl forces `1.1.1.1` and `1.0.0.1`; Raptor respects the provider's DNS and works where outside DNS is blocked. |
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
| `P_SERVER_ALLOCATION_LIMIT` | The server's allocation limit, printed as Pterodactyl's Wings prints numbers (`0.000000`); 0 until the Panel has allocation limits |
| egg variables | Every egg variable, name upper-cased. An egg variable can't override a built-in above. |

**Startup:** the yolks' entrypoint (`/entrypoint.sh` under `tini`) converts `{{VAR}}` in `STARTUP` to `${VAR}` and `eval`s it **inside the container**. Wings never expands or runs the startup command itself, and never on the host. The entrypoint expands the command with `eval echo` and runs it with `exec env`, not through a shell, so a startup must be a single command: `&&` or `;` run during the expansion, before anything is printed (found by the diff tests, and the same on Pterodactyl).

### Startup, running, and stop
- `startup` command with variable substitution. **Never executed through a host shell.**
- `config.startup.done`: string or list of strings. A console line **containing** one marks the server as running; a value prefixed with `regex:` is a regular expression instead. `strip_ansi` removes colour codes before matching. Eggs with no done strings are running as soon as they start.
- `config.stop`, as Pterodactyl interprets it: a value **not** starting with `^` is a console command (e.g. `stop`). A value starting with `^` is a signal: after removing one `^`, `C` or `SIGINT` → SIGINT, `SIGTERM` → SIGTERM, `SIGABRT` → SIGABRT, **anything else → SIGKILL** (so `^^C` is SIGKILL). An empty value uses Docker's normal stop. After the stop timeout, the container is killed.

### Config file parsers (`config.files`)
Applied by Wings before every start (`internal/eggs/configfile`). Pterodactyl's Wings re-serialises whole files, losing YAML comments and key order and collapsing duplicate INI keys. **Raptor's parsers edit only what they change**, so a file an owner has hand-edited keeps everything else.

| Parser | Key syntax | Behaviour |
|---|---|---|
| `properties` | property name | Java `.properties` syntax (separators `=`, `:`, whitespace; `#`/`!` comments; `\` line continuations; `\uXXXX` escapes, since Java reads these files as ISO-8859-1). Matching entries are rewritten in place, keeping the key's spelling and separator; missing keys are appended. |
| `file` | line prefix | Every line starting with the key is replaced by the value (which usually repeats the key: `server.port 28015`). CRLF endings are kept. |
| `ini` | `section.key` | The first dot outside brackets splits section and key; brackets are dropped; later dots belong to the key (`[/Script/Engine.GameSession].MaxPlayers`, `[SystemSettings].net.AllowEncryption`). No dot: the section-less part at the top. Every matching key is rewritten in place (keeping spacing and quotes); missing keys go at the end of their section, missing sections at the end of the file. |
| `json` | dotted path | Key order and number formatting are kept; indentation is detected. |
| `yaml` / `yml` | dotted path | Comments, key order, and anchors are kept; indentation is detected. Multi-document files are refused rather than truncated. |
| `xml` | element path | Dots or slashes separate elements from the root (`MyConfigDedicated.SessionSettings.MaxPlayers`). Segments can be `*` or have a predicate: `[@attr='v']`, `[@attr]`, `[2]`. A value `[attr='v']` sets an attribute (7 Days to Die's `property[@name='ServerPort']`). Comments, declarations, and namespaces are kept; new elements are indented like their siblings. |

Path syntax for `json`/`yaml`: `a.b.c`, `list[0].host` (or `list.0.host`), and `*` wildcards (`servers.*.address`). Missing keys are created for unconditional rules, as in Pterodactyl.

**Values:** for JSON and YAML, integers written as strings become numbers (`"{{server.build.default.port}}"` → `25565`) and booleans written as booleans in the egg stay booleans, as in Pterodactyl. A templated `"true"` (`"{{server.build.env.PVP}}"`) stays the string `"true"`, also as in Pterodactyl; an egg whose game needs a real boolean from a variable fixes the file in its startup (DECISIONS #358). Other formats write text.

**Conditions** (`if_value`): an exact value, or `regex:<pattern>` to replace only the matched part (Go regex syntax, with `$1` references in the value). Conditional rules never create missing keys.

**Where Raptor differs from Pterodactyl's Wings**, verified against Pterodactyl Wings 1.13.3 by the [behavioural diff](#behavioral-diff-against-pterodactyl):
- More than one `*` in a path matches at every level; Pterodactyl expands only the first `*` and creates a key literally named `*`.
- The `file` (text) parser resolves placeholders like `{{server.build.memory}}` (what the Panel rewrites `{{server.build.env.SERVER_MEMORY}}` to); Pterodactyl's text parser writes them unresolved.
- Edits are made in place (comments, key order, and formatting kept), where Pterodactyl re-serialises the file; the values written are the same.

Earlier Pterodactyl versions (read from source) also mishandled `list[0].key` paths and exact `if_value` conditions, and ignored `if_value` for `properties`, `ini`, and `xml`. Current Pterodactyl handles all of these as Raptor does; the diff test covers each.

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
- **Empty fails only `required` and `filled`.** A value that's empty after trimming skips every other rule. That's how Pelican validates: an empty field is validated as its default (an empty string), and Laravel only runs implicit rules on empty strings. Pterodactyl turns empty fields into null first, and null fails type rules like `string`, so Pterodactyl rejects some eggs' own defaults (e.g. SpongeVanilla's `FORGE_VERSION`, rule `string`, default empty). Pelican's behaviour accepts everything Pterodactyl's does, and more.
- **Sizes depend on type.** `min`, `max`, `between`, `size`, `gt`/`gte`/`lt`/`lte` compare the number when the variable also has `numeric` or `integer` and the value is numeric; otherwise they count characters.
- **`boolean`** accepts only `1` and `0` for string values (Laravel rejects the strings `true`/`false`; eggs that want those use `in:true,false`).
- **`regex:`** patterns are PHP (PCRE) with delimiters, including bracket delimiters (`regex:([a-z]+$)`) and modifiers `i m s U A u D`. They're translated to Go.
- **Supported:** `required`, `nullable`, `sometimes`, `present`, `filled`, `string`, `integer` (and `int`), `numeric`, `boolean`, `in`, `not_in`, `min`, `max`, `between`, `size`, `gt`, `gte`, `lt`, `lte`, `digits`, `digits_between`, `regex`, `not_regex`, `alpha`, `alpha_num`, `alpha_dash`, `url`, `ip`, `ipv4`, `ipv6`, `starts_with`, `ends_with`, `lowercase`, `uppercase`, `json`, `uuid`. That covers every rule in the 606 surveyed eggs.
- Parameters are trimmed (`in: a,b` is common in eggs) and may be quoted (`in:"a,b",c`), as Laravel's parser allows.
- Unknown rules and PCRE-only patterns (lookarounds, backreferences) are **skipped, not failed**, so an egg never becomes unusable; `Egg.Lint` reports them for the library.

Checked against reality: of the 606 eggs, the defaults that fail their own rules are empty values for `required` fields (tokens and passwords the owner must enter) and 10 checks in 8 eggs that are genuine egg bugs Laravel rejects too (a default of `moon` for `in:Moon,Mars`, placeholder text in a `digits_between` field, a port with `integer|max:8`).

### Features
`eula`, `java_version`, `pid_limit`, `steam_disk_space`, `gsl_token` and similar. These drive Panel UI prompts (e.g. "accept the Minecraft EULA") and Wings behaviour where applicable. Unknown features are ignored.

`java_version` matters in practice: the e2e tests found that the Pterodactyl-format Paper egg defaults to a Java 21 image, while current Minecraft needs Java 25. Pterodactyl detects the "requires Java" console message and prompts the user to switch images; Raptor does too (#421), from the server's console in the web app (`web/src/lib/problems.ts`): the library eggs' own refusal ("Choose Java 25 in Startup…") or Java's `UnsupportedClassVersionError` (class file version minus 44 is the Java needed). A stopped server whose newest console lines show one gets a card on its Overview to switch to the egg's matching image (the image named or tagged for that Java; with "or newer", the oldest that's new enough) and start it. Changing the image is a signed `server.update`, so the user's passkey signs it; members need the `startup` permission (and a delegation to sign), and `power` to start it. The egg library's Minecraft eggs check for themselves: the install writes the Java version that Minecraft version needs (from the server software's own API) to `.raptor-java`, and the startup command refuses to start on an older Java, saying which to choose in Startup ([library/README.md](../library/README.md#minecraft-java-eggs)).

### Inheritance
`config.extends` / `copy_script_from` (Pterodactyl legacy: parts taken from another egg by its Panel ID). Not resolved: those IDs mean nothing outside the Panel that exported the egg, so an import that uses them is refused.

## Raptor eggs

`RPTR_v1` is Raptor's own format: the eggs Raptor makes (the egg editor's new eggs and every egg in the [egg library](#egg-library), its test server included) are in it. It's Pelican's layout (an ordered `docker_images` map, `startup` or an ordered `startup_commands` map, the icon as a `data:` image in `image`, Pterodactyl's `config`, `scripts`, and `variables`) with **Raptor's settings as top-level fields** instead of under `x-raptor`:

```json
{
  "meta": { "version": "RPTR_v1" },
  "name": "Minecraft: Paper",
  "library": "minecraft/paper",
  "category": "minecraft",
  "arch": ["amd64", "arm64"],
  "players": { "query": "minecraft" },
  "docker_images": { "Java 21": "ghcr.io/xena-studios/yolks:java_21@sha256:…" },
  "startup": "java -Xms128M -jar {{SERVER_JARFILE}}",
  ...
}
```

The fields are those of [Raptor extensions](#raptor-extensions) below (`arch`, `category`, `install`, `backup`, `health`, `players`, `addons`, `workshop`, `web`, `modpack`), validated the same way, plus `library`, the egg's ID in the egg library. Errors name them without the `x-raptor.` prefix. An `x-raptor` block in a Raptor egg is ignored.

- **Nodes:** Wings lists the `raptor-eggs` capability when it reads the format. The Panel refuses a `server.create` or `server.update` with a Raptor egg for a node without it, saying to update Wings, rather than letting the node fail with "unsupported format" (DECISIONS #353).
- **Other panels** don't read `RPTR_v1`. Eggs imported from Pterodactyl or Pelican keep their format (and Raptor's settings for them stay under `x-raptor`, which those panels ignore); re-importing one over a Raptor egg keeps the Raptor egg's settings, moved under `x-raptor` (`spec.KeepRaptor`).

## Raptor extensions

In Pterodactyl's and Pelican's formats, Raptor's settings live under a namespaced key those panels ignore, so **Raptor-enhanced eggs still load elsewhere** (Raptor's own eggs have the same fields at the top level, [above](#raptor-eggs)):

```json
"x-raptor": {
  "arch": ["amd64", "arm64"],
  "backup": {
    "pre":  ["save-off", "save-all flush"],
    "post": ["save-on"],
    "wait_for": "regex:Saved the (game|world)",
    "timeout": "5m"
  },
  "health": { "type": "port", "protocol": "tcp" },
  "install": { "timeout": "3h" },
  "players": { "query": "minecraft" },
  "certified": true
}
```

`x-raptor` is validated when the egg is parsed: `install.timeout` must be a positive duration (`90m`, `3h`), `arch` entries must be `amd64` or `arm64` (`x86_64` and `aarch64` are accepted as aliases), and `players.query` must be `minecraft` (the Java edition's status ping, and the legacy ping servers before 1.7 answer) or `source` (Valve's A2S_INFO, which most Steam games answer). `players.port` says where the game answers queries when it isn't the primary port: an offset (`"+1"`) or a variable's name (`QUERY_PORT`). An invalid block rejects the egg rather than being half-applied.

### Backup hooks

`x-raptor.backup` is what a backup sends to a **running** server's console (as the user `backup`, so it shows in the console and the job's log):
- `pre`, before the snapshot (and before its databases are dumped). For Minecraft, `save-off` (no autosave writes during the copy) and `save-all flush` (everything in memory written out now), so the backup never catches region files mid-write.
- `wait_for`, the console line that says `pre` is done: a substring, or a regular expression after `regex:`, as done lines are, matched with the game's colours stripped. Wings waits up to `timeout` (a duration; the node's default is a minute). If the line doesn't come, the backup is taken anyway with a warning ("the server didn't confirm the save"): a possibly inconsistent backup beats none.
- `post`, after the snapshot, **whatever happened to it**. Optional: a game that can only save, not pause saving (Rust's `server.save`), has `pre` alone; one whose `pre` pauses saving (`save-off`, `save hold`) must have it, and the library refuses it without. a failed or cancelled snapshot, a stopping Wings, even a panic. A server left with saving off silently stops saving, which is worse than the bug the hooks fix. If Wings stops or crashes between the two, the job's checkpoint says so: the resumed job sends `post` before anything else, and a job given up on after repeated crashes gets it sent when Wings starts. A server that stopped meanwhile needs nothing (saving is on again after a start).

Stopped servers get no hooks; their files aren't changing. Safety and final backups (taken by a reinstall or a delete, with the server stopped) don't send them.

**Minecraft Java servers get the hooks without the block.** An egg with no `x-raptor.backup` that is stopped with `stop` and is started at the `)! For help, type` line (every Java server egg, Pterodactyl's and Pelican's included; not the proxies or Bedrock) gets `save-off`, `save-all flush`, waiting up to 5 minutes for `Saved the game` (`Saved the world` before 1.13), then `save-on` (`eggs.JavaMinecraftBackup`). So servers keep the egg they were made from ([DECISIONS.md](DECISIONS.md) #45) and still back up safely: imported ones, ones made before the catalog had hooks, and org eggs copied from them. An egg that sets the block, even to `{}`, gets exactly that.

**Not covered: Bedrock.** Its commands are `save hold`, then `save query` until it answers that the files are ready (with each file's length to truncate the copy to), then `save resume`. That needs polling and truncation the hooks can't express, so Bedrock servers are backed up without them, as before ([ROADMAP.md](ROADMAP.md#backlog)).

## Egg sources

- Raptor's egg library ([below](#egg-library)): Raptor's eggs for the games and apps people host, and its test server, seeded into the official library on every Panel start.
- Orgs make their own eggs (from scratch, or customised copies) and Raptor's staff publish official ones, in the egg library ([PANEL.md](PANEL.md#eggs)); editing writes into the egg file and keeps the format ([Editing eggs](#editing-eggs)).
- Users can import eggs from a URL or file. The Panel shows the **image registry and install script** before importing. A malicious egg is effectively a malicious program on the node. Imported eggs belong to the org that imported them ([PANEL.md](PANEL.md#importing)); eggs that leave parts to another egg (`config.extends`, `copy_script_from`) are refused with a message saying so, since those parts would be missing on the node.

## Editing eggs

The Panel's egg editor (`internal/eggs/spec`) reads an egg into fields (name, runtimes, startup commands, stop, ready lines, install script, variables, config files, features, and Raptor's `arch`, `players`, and `addons`) and writes edits back into the same file, Raptor's settings where its format keeps them (top-level in a [Raptor egg](#raptor-eggs), else `x-raptor`). A new egg starts as a Raptor egg (`spec.Blank`). Only those fields change; other keys stay, and so does each format's style: Pterodactyl's JSON-encoded `config.files`/`config.startup` and `a|b` rules, Pelican's objects and rule lists. Images are written as an ordered `docker_images` map, which every format reads. The output is JSON. Whitespace in ready lines and in the `file` parser's keys is kept: it's part of the match.

## Add-ons

An egg can turn on software add-ons for its servers, under `x-raptor.addons` (Raptor's own key; other panels ignore it):

```yaml
x-raptor:
  addons:
    plugins:                          # or mods, or datapacks
      loaders: [paper, spigot, bukkit] # Modrinth's loader names
      dir: plugins                     # where the files go, inside the server
      version: MINECRAFT_VERSION       # the variable with the game version (optional)
```

- **Plugins** (Paper, Spigot, Bukkit, Purpur, Folia, Velocity, BungeeCord, Waterfall, Sponge), **mods** (Fabric, Forge, NeoForge, Quilt), and **datapacks** (any Java server; their one loader is `datapack`). Plugins and mods are `.jar` files, and datapacks are `.zip` files in the world's `datapacks` folder (`world/datapacks` by default; an egg or server whose `level-name` isn't `world` sets `dir` to match). Each gives servers a section of their Add-ons tab (Plugins, Mods, Datapacks) to search Modrinth (and Hangar, for plugins), install a version, see what's installed, update, and remove (to the server's trash). Results are for the server's game version when its variable holds one (`1.21.1`), and any version when it says `latest`.
- In the egg editor they're the **Plugin installer**, **Mod manager**, and **Datapack manager** cards on the Add-ons tab (the CPUs, `arch`, are on the Overview tab). The library's Minecraft eggs set them in `raptor.yaml` (`addons:`), added to the egg like `players`. Every Java server egg has datapacks; the proxies (Velocity, BungeeCord) don't.
- **Modrinth and Hangar.** Both publish file hashes. Plugins for Paper (and the servers that load its plugins), Velocity, and BungeeCord or Waterfall can also be found on **Hangar**, PaperMC's repository: the Plugins page has a Modrinth/Hangar switch. SpigotMC isn't offered: its downloads often go to outside sites without hashes, so Wings couldn't check what it fetched.
- **CurseForge** (#412, `internal/panel/curseforge`): mods (Forge, Fabric, Quilt, NeoForge) and Bukkit plugins (Paper's family), as a third choice of the switch. It's off until the Panel has `PANEL_CURSEFORGE_API_KEY` (from the CurseForge for Studios console, kept on the Panel only); `AddonService.ListAddonSources` says whether it's on, and the app offers it only on nodes with the `addons-curseforge` capability. Wings downloads from `edge.forgecdn.net` and `mediafilez.forgecdn.net`, checked by the SHA-1 CurseForge lists. A mod whose author turned off third-party downloads (`allowModDistribution: false`, or a file with no `downloadUrl`) is never installed: its versions carry `manual_url` (the file's page) instead of a URL, and the app links there to download it by hand. Installed files are named by CurseForge's fingerprint (MurmurHash2, seed 1, of the file without its whitespace bytes), which `addons.list` gives as `cf_fingerprint`, for files neither Modrinth nor Hangar knows; an update is the newest file for the server's loader and game version, when it's newer. Its dependencies are resolved like Modrinth's (#434; `version_id` is `mod@file`): required ones and theirs at the newest file for the server's loader and game version, its own optional ones, installed ones it can't run beside; a dependency whose author blocks other apps' downloads is listed as unavailable, to download by hand.
- **What's installed** is named from Modrinth by each file's SHA-512, then, for files Modrinth doesn't know, from Hangar by its SHA-256 (`addons.list` gives both). Hangar looks up one file per request, so at most 50 a listing. Each named file shows as up to date or with an update available (the newest version for the server's loaders and game version), and the list counts the updates; **Update all** installs them one after another after a confirmation listing each change, reporting any that failed. An update is offered for a Hangar file only when the file is one of the project's listed versions, so it's never a step back. Files neither knows show as added by hand.
- **Dependencies** (#434, Modrinth): `AddonService.ResolveAddonDependencies` walks a version's `dependencies` on the Panel: required ones and theirs (the version named, or the project's newest for the server's loaders and game version), optional ones only the version's own, embedded ones skipped, at most 25 projects. Projects already installed are left out, and installed ones it lists as incompatible are named. The install dialog installs the required ones first (each checked by its own hash, as any install), offers the optional ones, refuses while an incompatible one is installed, and names required ones it can't install (no version for the server, or outside Modrinth). "Install only this" skips all of it. Installed files whose version lists a required dependency that isn't installed get a badge and a button to install it; removing one that others need asks first. Hangar's plugin dependencies aren't read yet.
- **Client-only mods** (Modrinth's `server_side: unsupported`: shaders, minimaps) are left out of mod search by default (`server_side:required`/`optional` facets); "Show client-only mods" (`include_client_only`) brings them back, marked.
- **Factorio's mods** (the `factorio` kind, loader `factorio`, `.zip` files in `mods/`, #411) come from its mod portal. Its API has no search, so the Panel keeps the whole list (about 10,000 mods, refreshed hourly) and searches it (`internal/panel/factorio`), most downloaded first, for the server's Factorio version (`FACTORIO_VERSION`'s major.minor; latest or experimental: any). Releases come with a SHA-1 and dependencies (`? optional`, `(?)` hidden optional, `! incompatible`, `~` no load order), which `ResolveAddonDependencies` (`source: "factorio"`, version IDs `mod@version`) walks like Modrinth's, leaving out the base game and Space Age's parts. Installed mods are named by file name (`Krastorio2_2.1.4.zip`) and checked by SHA-1. Downloads need a factorio.com account: the egg's add-on names the variables (`auth: {username: SERVER_USERNAME, token: SERVER_TOKEN}`) and Wings adds them to the download itself (DECISIONS #362); with them empty, it says to set them in Startup. Factorio enables every mod in `mods/` it finds, so `mod-list.json` isn't touched. The egg's `UPDATE_MODS` still updates everything listed at a reinstall. Only nodes with the `addons-factorio` capability get the page.
- **Rust's plugins** (the `umod` kind, loaders `oxide` and `carbon`, #409) come from uMod: `.cs` files, checked by the SHA-1 uMod lists for each plugin's newest file (the only file and hash it publishes). Oxide loads them from `oxide/plugins` and Carbon from `carbon/plugins`, so the Rust egg's add-on sets `dir_variable: FRAMEWORK` and `dirs: {carbon: carbon/plugins}`: Wings picks the folder from the server's variable when listing and installing (any add-on can do this; the variable must be one of the egg's). Installed plugins are named by file name; one whose SHA-1 isn't the newest file's is offered it. The page says to choose Oxide or Carbon when the framework is vanilla. Only nodes with the `addons-umod` capability get the page; older Wings ignore the kind.
- Installing is the node's `addons.install` (the `files.write` permission): Wings downloads from `cdn.modrinth.com`, `hangarcdn.papermc.io`, `umod.org`, `mods.factorio.com`, and CurseForge's CDN only, over https (redirects too), keeps the file only if its hash is the one the Panel passed from the source's listing (a SHA-512 from Modrinth's CDN, a SHA-256 from Hangar's), and writes it through the server's file system like the file manager (symlink-safe, the denylist, the disk limit). A file from Modrinth's CDN always needs its SHA-512. Hangar versions hosted elsewhere (an external link, no hash) aren't offered. The Panel finds things (`AddonService`, which asks Modrinth and Hangar with its own user agent and caches answers for 5 minutes), so the browser talks only to Raptor; only the projects' icons load from their CDNs.

## Add-on lists

Each add-on kind's section in Add-ons has a menu (**⋯**) with **Export list** and **Import list** (#437, `web/src/lib/addon-list.ts`, `web/src/components/addon-list.tsx`): add-ons as code, for networks that run the same plugins on several servers, or the same mods on test and live. It's the add-on part of servers-as-code deploys (#374), where a repo would hold the list and a deploy apply it.

```json
{
  "version": 1,
  "kinds": {
    "plugins": {
      "loaders": ["paper", "spigot", "bukkit"],
      "game_version": "1.21.4",
      "addons": [
        {
          "source": "modrinth",
          "project": "Vebnzrzj",
          "version_id": "fEQ2Bc8t",
          "name": "LuckPerms",
          "version_number": "v5.4.145-bukkit",
          "file": "LuckPerms-Bukkit-5.4.145.jar",
          "sha512": "…"
        }
      ],
      "by_hand": [{ "file": "OurPlugin.jar", "sha512": "…" }]
    }
  }
}
```

- **`version`** is the format's (1). A list with a higher one is from a newer Raptor and is refused, not half read. **`kinds`** is keyed like the egg's `addons` (plugins, mods, datapacks, umod, factorio); exporting writes the section's own kind, and importing reads only that kind (a list for another kind says which it has).
- **Each add-on is named, never linked:** `source`, `project` (the source's project ID: Modrinth's ID, Hangar's slug, uMod's slug, the Factorio mod's name, CurseForge's ID), and `version_id` (Factorio's is `mod@version`). `name`, `version_number`, and `file` are for people reading it. The hashes (`sha512`, `sha256`, `sha1`) are the file's as the source lists it. There's no URL field: applying asks the Panel for the version again (`AddonService.ListAddonVersions` with the server's loaders and game version), installs through `addons.install` with the hash the Panel lists, and Wings checks it as on any install. A listed hash that no longer matches the source's ("its file changed at the source") is refused.
- **`by_hand`** is the files no source knows (added by hand): name and SHA-512 only. Raptor can't fetch them, so importing lists those the server lacks as **Copy these yourself**.
- **Applying shows the diff first** in a dialog, and changes nothing until **Apply**: to install (with required dependencies neither the list nor the server has, from `ResolveAddonDependencies`, for Modrinth, Factorio, and CurseForge), to change version (newer or older: the list wins), to remove (identified add-ons not in the list, and second copies of one; a switch keeps them; removed files go to the trash), already there, and kept (files added by hand on this server, which the list can't name, are never removed). Add-ons match by source and project; hand-added files by SHA-512.
- **A version the server can't install** (not listed for its loaders and game version, or past the 50 newest that are listed; uMod has only its newest; CurseForge files their authors keep to CurseForge) is skipped and named, unless **use the newest for this server** is on. When the list came from other loaders or another game version, the dialog says so.
- It's all in the browser on the existing calls: nothing new on the Panel or Wings, and no permission beyond `files.read` (export) and `files.write` (import), like the rest of the tab. Applying one list to several servers at once, and the same through API tokens and MCP (#374), are still to come.

## Server icon and MOTD

Minecraft Java servers (an egg whose `players.query` is `minecraft`) that have a `server.properties` get **In the server list** on the Game tab (#425, `web/src/components/server-list-look.tsx`): the icon and MOTD players see, previewed as the client draws them. The icon is any image, cropped square and drawn to a 64×64 PNG in the browser, written as `server-icon.png` (or removed); the MOTD has colour and style buttons that insert § codes, and is written to `server.properties` the way Minecraft writes it (non-ASCII as `\uXXXX`, the line rewritten in place, `web/src/lib/minecraft-text.ts`). Both go through `files.write` (and `files.delete`), so they need that permission. Proxies (no `server.properties`) don't get it; their MOTD is in their own config. It's on the Game tab with the settings form (#424).

## Web dashboards

An egg whose server serves a web page (txAdmin, Grafana, code-server) says so under `x-raptor.web` (#428), and the server's Overview links to it (**Open txAdmin**):

```yaml
web:
  name: txAdmin              # what it's called
  port: TXHOST_TXA_PORT      # the variable with its port; omitted: the primary port
  enabled: TXADMIN_ENABLE    # a variable that turns it on (1 or true); omitted: always on
```

- The link is `http://<the server's address>:<port>/`: the shown address's host if it has one, else the node's hostname. A port variable that's empty means it's off. If the port isn't one of the server's ports, Overview says to add it on Network instead of linking.
- Both variables must be the egg's. The library sets it for FiveM's txAdmin and the software eggs with a web page (code-server, Grafana, Uptime Kuma, Forgejo, Gitea, Owncast, yarr, Reposilite, Prometheus, RabbitMQ's management page, Game Server Watcher, TS3 Manager).
- Wings reports it in the egg summary (`web`), so servers on older Wings show no link until they update.

## Modpacks

Modpack eggs name the variables that pick their pack under `x-raptor.modpack` (#413), so the app can offer a pack picker instead of IDs typed by hand:

```yaml
modpack:
  source: modrinth          # where packs are found: modrinth, ftb, or technic
  project: PROJECT_ID       # the pack's slug or ID
  version: VERSION_ID       # a version's ID, or latest
  game_version: MINECRAFT_VERSION   # optional: a game version the chosen version is for
```

- **Raptor downloads nothing:** the picker fills the variables, and the egg's install script fetches the pack, as with IDs typed in. The variables must be the egg's; `source` must be `modrinth`.
- The picker (`web/src/components/modpack-picker.tsx`) is on the new-server form and on Startup (for whoever may edit the pack's variable). It searches Modrinth's modpacks (`AddonService.SearchAddons` and `ListAddonVersions` with kind `modpacks`, which need no loader) and offers **the newest** (`latest`, updated at each reinstall) or a version, which sets the game version to one it's for, since the Modrinth egg filters versions by it. On Startup it says to save and reinstall, with a backup first.
- The library's Modrinth, FTB, and Technic modpack eggs set it (FTB: `FTB_MODPACK_ID` and `FTB_MODPACK_VERSION_ID`, where an empty version is the newest; Technic: `TECHNIC_PACK` and `TECHNIC_VERSION`, recommended or latest). FTB's and Technic's packs come from their APIs through the Panel (`internal/panel/modpacks`, `SearchAddons`/`ListAddonVersions` with source `ftb` or `technic`, the modpacks kind only): FTB's most installed when nothing's typed, its private packs and versions left out; Technic's by name. CurseForge's (#412) come later. The summary Wings reports carries it (`modpack`), so servers on older Wings show no picker on Startup until they update; the new-server form reads it from the catalog.

## Bedrock players (Geyser)

Servers whose egg has the plugin installer (Paper's family, Velocity, Waterfall/BungeeCord) or the mod manager (Fabric, NeoForge) get a **Bedrock players** card on Network (#419, `web/src/components/bedrock-players.tsx`, `web/src/lib/geyser.ts`). It installs Geyser from Modrinth and Floodgate from Hangar (plugins) or Modrinth (mods) through the ordinary `addons.install`, so each file is checked by its source's hash; Floodgate is left out on proxies (its key must be on every backend). Geyser writes its config on its first start; **Finish setting up** then puts Bedrock on the server's own port: `bedrock.clone-remote-port: true` for the plugins (Geyser follows the Java port at every start), `bedrock.port: <the primary port>` for the mods. No new allocation is needed, because every allocation is published for TCP and UDP. With Floodgate and `remote.address: auto` (the default), Geyser picks Floodgate's auth itself. It needs `files.read` to see and `files.write` to change.

## Bedrock packs

Servers whose egg is the library's Bedrock or Endstone (by `egg_source`, or by name for older servers) get a **Packs** section in Add-ons (#416, `web/src/components/bedrock-packs.tsx`, `web/src/lib/bedrock-packs.ts`, `web/src/lib/zip.ts`). It needs no egg block: Bedrock's layout is fixed. The browser reads the `.mcpack`/`.mcaddon` (zip central directory, deflate through `DecompressionStream`, nested `.mcpack` files one level deep) to find each `manifest.json` (comments allowed; `resources` modules make a resource pack, `data`/`script` a behaviour pack; a `script` module marks it as needing Beta APIs). Each archive is uploaded as a temporary zip, unpacked with `files.decompress`, each pack moved with `files.rename` to `behavior_packs/` or `resource_packs/<name>_<uuid prefix>` (rename never replaces, so a same-named folder fails rather than being overwritten), and added to `worlds/<level-name>/world_{behavior,resource}_packs.json` with its version. Nothing goes through Wings' download allowlist: the file comes from the user, like any upload. Mojang's own packs (`vanilla*`, `chemistry*`, `experimental*`, `editor*`) are hidden. It needs `files.read` to see and `files.write` to change.

## Steam Workshop

Games that download Steam Workshop items themselves take them under `x-raptor.workshop` (DECISIONS #264):

```yaml
x-raptor:
  workshop:
    app_id: 4000          # the game whose Workshop the items are from
    variable: WORKSHOP_ID # the egg variable the game reads them from
    collection: true      # one collection's ID; otherwise items' IDs, comma-separated
    apply: restart        # or reinstall, when the install script downloads them
```

- **Raptor downloads nothing.** The game (`+host_workshop_collection`, `-automanagedmods`) or the egg's install script (`workshop_download_item`) fetches the items; Raptor edits the variable. So there's no new download path on the node, and nothing for Wings to check: what runs is what the game would run with the same IDs typed into Startup.
- Servers get a **Workshop** section in their Add-ons tab (`/addons/workshop`; the old `/workshop` address redirects there; for those with the `startup` permission, which changing variables already needs): the items or collection named from Steam with their pictures, a box that takes a Workshop link or ID, shows what it is, and refuses items for another game; for games that take a list, a collection's link adds its items. Saving is `server.update` with the variable, unsigned like any variable. The page says whether a restart or a reinstall picks the change up.
- The Panel names the IDs (`AddonService.LookupWorkshopItems`, from Steam's public `ISteamRemoteStorage` API, which needs no key; cached like Modrinth). Items that aren't public, or are banned, are left out. Previews load only from `images.steamusercontent.com`, which the app's CSP allows. There's no search: Steam's search API needs a key, and the Workshop's own site is better at browsing, so the page links to it.
- The library sets it in `raptor.yaml` (`workshop:`) for Garry's Mod (a collection, at start), Counter-Strike 2 (a map collection, at start), ARK (mods, at start), Conan Exiles (mods in load order, at reinstall), and Squad (items, at reinstall). `variable` must be one of the egg's variables, and `apply` `restart` or `reinstall`; anything else rejects the egg.

## CS2 frameworks

The library's Counter-Strike 2 egg installs **Metamod:Source** and **CounterStrikeSharp**, the frameworks CS2 server plugins run on (#414, DECISIONS #370). They're egg variables, not a Raptor download: `METAMOD` and `COUNTERSTRIKESHARP` (booleans, off by default) and `METAMOD_VERSION` and `CSS_VERSION` (`latest`, or a Metamod build number such as `1491`, a CounterStrikeSharp release such as `1.0.380`).

- **The install script downloads them** (on the installer image, as every egg fetches its game files): Metamod's build from `mms.alliedmods.net/mmsdrop/2.0` (`mmsource-latest-linux` names the newest; a pinned build is found in the folder's listing), CounterStrikeSharp's `counterstrikesharp-with-runtime-linux-<version>.zip` from its GitHub releases with the installer's `gh-release`, which checks GitHub's SHA-256 (the script refuses an asset without one). AlliedModders publishes no checksums, so Metamod is fetched over HTTPS only, the same trust as SteamCMD's own download. Each is unpacked in `.raptor-build` and copied into `game/csgo/addons`; an archive without the expected CS2 files is refused. What the owner set up stays: `metaplugins.ini`, CounterStrikeSharp's `configs` and `plugins`. CounterStrikeSharp's `api`, `bin`, and `dotnet` are replaced whole, so an old .NET doesn't linger. CounterStrikeSharp brings Metamod with it, since it's a Metamod plugin.
- **The startup loads Metamod**, before the game: CS2's updates replace `game/csgo/gameinfo.gi`, and the image's entrypoint updates the game just before the startup runs, so patching it there means no update can undo it. With either switch on and Metamod's CS2 binary present, it adds `Game csgo/addons/metamod // added by Raptor (Metamod:Source)` after the `Game_LowViolence` line, once (any line naming `csgo/addons/metamod`, a hand-added one too, counts). Not installed yet, or no `Game_LowViolence` line, it says so in the console and the game starts without it.
- **Turning off takes effect at the next start**, and Raptor only undoes what it did: Metamod off removes the line it added (a hand-added one stays); CounterStrikeSharp off renames `addons/metamod/counterstrikesharp.vdf` to `.vdf.off` only when Raptor installed it (`addons/counterstrikesharp/.raptor`), and turning it on renames it back. Files are never deleted. Turning one on or changing a version needs a reinstall, which also brings the newest builds after a CS2 update breaks them.
- **The app** gives such servers a **Frameworks** section in Add-ons (`web/src/components/cs2-frameworks.tsx`; any egg with all four variables): a switch for each, a version box, and **Save and reinstall** (a `server.update` with the variables, then `server.reinstall`, which backs the files up first) or just **Save** when only turning off. It needs the `startup` permission, and `reinstall` to start the install.
- **CounterStrikeSharp's plugins** aren't an add-on kind yet: there's no central repository with hashes, only each author's GitHub releases. A generic "from a GitHub release" source, checked by GitHub's digest, is the later step (#414).

## Egg library

`library/` in the repository is Raptor's one egg library: Raptor's eggs for the games and apps people host, and Raptor's own test server. The `library` Go package (`github.com/xena-studios/raptor/library`) embeds it, the Panel seeds it into its official egg library on **every start** ([below](#in-the-official-library)), and the conformance suite runs the same files through Wings. There is nothing to import by hand (DECISIONS #357). It covers every egg on eggs.pterodactyl.io, in the pelican-eggs repositories, and in FeatherPanel's, plus Pterodactyl's built-in eggs, with variants folded into one egg as options.

One folder per egg, `<category>/<egg>/`. **The folder is the egg's ID** (`minecraft/paper`), its `catalog_id` in the official library, and never changes. The categories are the library's (`minecraft`, `steam`, `standalone`, `software`, `voice`, `generic`). Each folder has:

- The egg file: Raptor's own [`RPTR_v1`](#raptor-eggs) JSON (`source.repo: xena-studios/raptor`, no commit, `source.path` is `library/<id>/<file>`). The loader still accepts an unmodified upstream copy at a pinned commit (`source.repo`, a full `source.commit`, `source.path`, upstream's file name), with its licence kept, though the library has none today.
- `icon.png`: the game's own icon, 256×256, shown instead of any icon in the egg file. The seed gives it to the official egg whenever it changes, unless staff uploaded one ([PANEL.md](PANEL.md#images)).
- `raptor.yaml`: the source, licence, `credits` (what the egg is based on, with the licence, and where its icon came from; pelican-eggs and Pterodactyl's eggs are MIT, FeatherPanel's have no licence so they're only a reference), whether it's `certified`, whether it's `internal`, the CPU architectures the game runs on, Raptor's settings, and how the conformance suite tests it:

```yaml
source:
  repo: xena-studios/raptor
  path: library/minecraft/paper/egg-paper.json
license: MIT
credits:
  - "Based on the Paper egg in https://github.com/pelican-eggs/minecraft (MIT); icon from PaperMC"
certified: true
internal: false     # true: Raptor's own tool (the test server), not listed in orgs' libraries
arch: [amd64, arm64]
players:            # optional: how to ask the game for its player count
  query: minecraft  # added to the egg servers are made from (top-level in RPTR_v1, else x-raptor)
addons:             # optional: the plugin installer or mod manager (see Add-ons), added the same way
  plugins:
    loaders: [paper, spigot, bukkit]
    dir: plugins
    version: MINECRAFT_VERSION
backup:             # optional: the backup hooks (see Backup hooks), added the same way
  pre: ["save-off", "save-all flush"]
  post: ["save-on"]
  wait_for: "regex:Saved the (game|world)"
  timeout: 5m
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

**Raptor's test server** (`generic/raptor-test`) isn't a game, but the quickest check that a node installs, starts, reaches, talks to, and stops a server: it serves a page on the server's port (its `MESSAGE` variable) and answers `status` on the console (DECISIONS #351). It's AGPL like the rest of Raptor, and `internal`: seeded so staff and the conformance suite have it, but orgs' libraries don't list it.

**Checks.** `go test ./library` loads every egg and checks it: the egg parses, the source matches the file, test variables pass the egg's rules, a chosen image is one the egg offers, and `raptor.yaml`'s `players`, `addons`, `workshop`, and `backup` reach the egg servers are made from. `backup` needs `pre` and a `wait_for`, and a `post` when `pre` pauses saving. It also holds every egg to the **house style** ([library/README.md](../library/README.md#how-eggs-read)): names are what people call the game (`Node.js`, `Minecraft: Paper`, no "server", "generic", or versions; real product names that contain one, such as Raptor's test server, are listed), descriptions are plain sentences without links or panel names, variables read alike, the author is `support@raptorpanel.net`, the licence is MIT (Raptor's test server: AGPL-3.0), every image (runtimes and the install container) is Raptor's (`ghcr.io/xena-studios/yolks`) or pelican-eggs' pinned by digest, and every egg has a 256×256 icon. `task library:pin` (`cmd/eggpin`) pins images to the digests their tags point to now; `-check` fails instead of changing anything. The editor's round trip is tested on the library and on real community eggs kept as fixtures in `internal/eggs/testdata` (every Pterodactyl and Pelican format version, JSON and YAML).

**Left out:** database eggs (Raptor's managed databases cover them) and games whose server needs an account to download (DayZ, Arma 3, Starbound; Hytale), until there's a Steam-account feature.

### In the official library

The library lives in the Panel's database as official eggs (`internal/panel/catalog`), seeded when the Panel starts, in one transaction under an advisory lock, so two Panels starting together don't race. Each egg's file is the library's with `raptor.yaml`'s `arch`, `category`, `players`, `addons`, `workshop`, and `backup` added (top-level in an `RPTR_v1` egg, else under `x-raptor`), in the same format and key order; the row keeps the egg's ID (`catalog_id`), whether it's `certified`, and whether it's `internal`.

- An egg the official library doesn't have is added, published, as version 1 by "Raptor" ("From Raptor's egg library").
- When the library changes (a deploy), an egg **the seed owns** gets the new file as a new version by "Raptor" ("Updated from Raptor's egg library"); a change to only its icon or `raptor.yaml` settings updates those without a version. The seed owns an egg whose newest version is its own, or one imported by hand from the egg library before the Panel seeded it (taken over on the first start, its origin becoming `catalog`).
- **An egg staff changed is theirs.** Once its newest version is by anyone else, the seed leaves it exactly as staff left it, through any number of deploys. It follows the library again only when staff put back a file the seed wrote (a version by "Raptor"); from the next start on, library changes reach it again.
- What the seed last wrote is kept as a hash (`library_sum`: the file, its icon, and its settings), so a start where nothing changed is one query and a hash per egg: no icons decoded, no rows written. Icons that do need saving are decoded side by side.
- **Internal eggs** (`internal: true`, only the test server) are official eggs like the rest: staff see and manage them, the conformance suite and anyone with the ID find them, servers made from one keep working. Orgs' library listings skip them.
- Editing a certified egg's file clears `certified`, since it's no longer the file the conformance suite tests.
- Servers made before the catalog was in the library name the egg by its ID; the library finds it by that ID too, and counts those servers.
- An egg the library no longer has (**retired**) is **deleted if no server was made from it** (by its row's ID or its library ID) and the seed still owns it; one a server uses stays exactly as it is, so the server keeps working and can still be updated or reinstalled from it. Staff's own official eggs, and eggs staff changed, are never touched.
- **Importing** a file whose `library` ID is one of the library's into the official library is refused ("the Panel keeps it up to date itself"); a file with an ID the library doesn't have (an old export, another Raptor's) still becomes the newest version of the official egg with that ID, or a new one with it (DECISIONS #354). An org's import is its own and matches nothing. `task library:export` (`cmd/eggexport`) still writes one importable file per egg, with its icon as a `data:` image and its `category` and `library` ID, for another Panel.

Servers get the file in the passkey-signed `server.create` as it is (`EggService.GetEggFile`), so what the user approved is what the node runs.

A server keeps that file. A new library version doesn't touch existing servers: their Settings page shows which version they're on (the web app finds the server's `egg_hash` among the SHA-256s of the egg's versions; none matching: an edited copy) and that it's out of date when the newest version's isn't it. Admins can move a server to any version, newer or older, seeing what changes between the two versions' specs (`GetEggDetails` with `version`). That's a signed `server.update` with the chosen file, reinstalling only if asked; the Reinstall card can take the newest version in the same update, opt-in ([SERVERS.md](SERVERS.md), DECISIONS #339, #341). Library eggs are `RPTR_v1`, which only Wings with the `raptor-eggs` capability runs; the Panel refuses to send one to an older node, with a message to update it (DECISIONS #353).

### Accepting a EULA
Eggs with the `eula` feature (Minecraft's) need the user to accept the game's EULA. The web app asks when creating the server and sends `accept_eula: true` in `server.create`, inside what the passkey signs. Wings writes `eula.txt` (`eula=true`, owned by the server's user) once the install succeeds and before the first start. For eggs without the feature the flag does nothing.

## Conformance test suite

`internal/wings/conformance` runs the egg library's eggs through the full Wings lifecycle with the real server manager, Docker, and the eggs' unmodified images:

**install → EULA and uploads → start → done string → console command → backup hooks → Wings restart → stop → reinstall → start → stop → delete**

- **Backup hooks:** for an egg with `backup` in `raptor.yaml`, the `pre` commands are sent and the real game must print the `wait_for` line within `timeout`; then `post`.

- **Wings restart:** the manager is closed and a new one started on the same state, as a Wings restart does. The server must keep running (same container start time), its console history must be refilled, and commands must still work.
- **Stop** must be the egg's own stop, not a kill: Wings reports a stop that hit its timeout (`[raptor] didn't stop within 1m0s; killed`), and the suite fails on it.
- **Reinstall** runs the install script over the existing files, then the server must start again.
- Each step is timed; a Markdown summary goes to `RAPTOR_CONFORMANCE_REPORT` (the job summary in CI).
- Failures are never retried away. On a failed install or start, the install log and console are printed. Known upstream flakiness: NeoForge's install fails occasionally (its script fetches version lists and the installer downloads many libraries without retries); its test pins exact versions, which removed most of it.

**Tiers** (`test.tier`):
| Tier | Eggs | When |
|---|---|---|
| fast | The test server; eggs that install quickly (Minecraft, apps, languages) | Nightly and on demand (a PR runs the ones it changes, and the test server); amd64 and arm64 runners |
| slow | SteamCMD games (Rust, Valheim, …) | Weekly and on demand; amd64 |
| manual | Eggs too big for a runner's disk or needing an account (Counter-Strike 2, about 60 GB) | By hand |

A PR that touches eggs or the code that runs them runs the test server and the library eggs it changes, whatever their tier except `manual`, on amd64 and arm64 (`RAPTOR_CONFORMANCE_IDS`). The whole fast tier runs nightly in four shards per CPU, the slow tier weekly in four. Every library egg is certified, so every run must pass (`RAPTOR_CONFORMANCE_SET` still picks certified or community eggs, for when there are both).

```bash
task e2e:conformance                         # fast tier in the VM
task e2e:conformance TIER=slow RUN=steam/rust
```

Hostile eggs (planted symlinks, FIFOs, and links to host files) are `TestPathSafety` in `task e2e:runtime` ([SECURITY-MODEL.md](SECURITY-MODEL.md#server-files)).

### Behavioral diff against Pterodactyl

`TestPterodactylDiff` (`task e2e:pterodactyl`; in CI on PRs that touch eggs or the code that runs them) runs the **real** Pterodactyl stack beside Raptor: the Panel (with its database and cache) in Docker and Pterodactyl's Wings on the host, set up by `scripts/pterodactyl/setup.sh` through the Panel's own commands and services. Using the real Panel matters: it rewrites eggs' config file rules and resolves some placeholders before Wings sees them, and a stand-in would only test a copy of that logic.

For each case the same egg, variables, image, and port run on Pterodactyl, then on Raptor, and the test compares:
- the container: environment, startup command, user, hostname, mounts, tmpfs, capabilities, security options, limits, logging, DNS, ports;
- the config files the egg's rules wrote, by value (a properties map, the YAML/JSON tree, INI sections, the XML tree, text lines), since Raptor edits in place and Pterodactyl re-serialises.

Every difference must be listed as intended, with its reason, and every listed file difference must actually happen: when Pterodactyl changes, the test fails instead of letting the docs go stale. Cases: a test egg that exercises every parser, placeholder form, and condition (`internal/wings/conformance/testdata/parsers.ptdl_v2.json`), and Paper.

The first run found two gaps, both fixed: Raptor didn't mount `/etc/machine-id`, and set `P_SERVER_ALLOCATION_LIMIT` to the allocation count instead of the limit in Pterodactyl's number format. It also showed that four of the five "Pterodactyl is broken here" cases these docs listed had since been fixed in Pterodactyl.

Fuzz tests (`go test -fuzz`) cover egg parsing, rule validation, PHP regex translation, placeholder resolution, every config parser (output must always re-parse), `.properties` round trips, and config file paths (nothing outside the server directory is ever touched). Inputs the fuzzer found are kept in `testdata/fuzz` and run as regular tests.

## Community eggs

Catalog entries can be `certified: false`: community eggs tested the same way, whose CI jobs report failures in the job summary without failing the build (an upstream egg breaking shouldn't block Raptor's PRs, but it should be seen). There are none now.

**The first catalog** (October to November 2026, 64 upstream eggs, 46 of them community ones) was removed to start over (DECISIONS #351); what was learned building it still applies. Nobody publishes egg download counts, so its list was built from the maintained [pelican-eggs](https://github.com/pelican-eggs) repositories (317 eggs; Raptor's parser reads 316, the other is an empty file upstream):
- Games with large player bases that people self-host, plus a few widely used non-game servers (databases, Lavalink, Forgejo). Recent upstream commits were a tiebreaker.
- Anonymous downloads only: eggs that need a game account to install (DayZ, Starbound, Assetto Corsa's Steam variant) were left out. Eggs that need an account token only to run (Don't Starve Together, Eco, TF2, Unturned) are in the **manual** tier.
- Windows-only servers run under Wine or Proton are included where the egg does it (Enshrouded, V Rising, Abiotic Factor, Conan Exiles); they're x86-only.
- The upstream arm64 variants of Bedrock and Factorio (x86 servers run through box64) are included for arm64 coverage.

**An egg that doesn't work isn't in the catalog.** The first catalog's runs found these broken (as of the pinned commits); they were dropped, and are worth reporting or fixing upstream:

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
| Project Zomboid | SteamCMD fails every time with "Missing configuration" for the server app (three runs); earlier, killed by the OOM killer at 4 GiB |
| Risk of Rain 2 | The install doesn't produce the server executable (`Risk of Rain 2.exe` missing) |
| Pavlov VR | Not broken, but needs an API key from the developer; not added |

Eggs kept with a workaround in their test settings (each explained in its `raptor.yaml`): Forge, NeoForge, and Forgejo pin versions their scripts can install; TShock and several Minecraft eggs pick a newer image the egg offers; Palworld waits for its real ready line. Known flaky: NeoForge (see above).

## Catalog rules

What the egg library takes:
- **Hytale isn't certified.** Its official server image signs in to a Hytale account (OAuth device flow) before it downloads the server, so it can't be tested unattended. It isn't in the library either; owners can import a Hytale egg and sign in with their own account from the console.
- Most Steam dedicated servers are **x86_64-only** (Rust, CS2, …). The Panel hides them on arm64 nodes. No x86 emulation in v1.
- Only **anonymous SteamCMD** games in v1. No storing Steam credentials.
- Minecraft: users must accept the EULA. Server jars are downloaded at install time and never redistributed.
- Windows-only servers aren't certified. A few run under Wine or Proton as [community eggs](#community-eggs) where the upstream egg does it.
