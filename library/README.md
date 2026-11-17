# Egg library

Raptor's one egg library: eggs for the games and apps people host, made by
Raptor, and Raptor's own test server (`generic/raptor-test`). The Panel
embeds this folder and seeds it into its official egg library on every
start, so a merged egg reaches users with the next deploy; nothing is
imported by hand. See [docs/EGGS.md](../docs/EGGS.md#egg-library).

One folder per egg, `<category>/<egg>/`:

- `egg-<egg>.json`: the egg, in Raptor's own format (`RPTR_v1`,
  [docs/EGGS.md](../docs/EGGS.md#raptor-eggs)).
- `raptor.yaml`: `source` (this repository, the egg's path, no commit),
  `license: MIT`, `credits` (what it's based on, with the licence), `arch`,
  Raptor's settings (`players`, `addons`, `workshop`, `backup`), and its
  conformance `test`. `internal: true` keeps an egg out of orgs'
  libraries (only the test server, which is also AGPL like Raptor).
- `icon.png`: the game's own icon, square, 256×256. Where it came from is
  in `raptor.yaml`'s `credits`.

**The folder is the egg's ID** (`minecraft/paper`), and it never changes:
it's the official egg's ID in the Panel, so a changed egg becomes a new
version of the same official egg (servers on it move when an admin
chooses), and an egg staff edited in the Panel stops following this
folder. Keep the old catalog's IDs for the games it had. Removing a folder
retires the egg: the Panel deletes it if no server was made from it.

`go test ./library` checks every egg against the rules below, and
`task e2e:conformance RUN=<id>` runs one through Wings.
`task library:export` writes one importable file per egg (the egg with its
icon and Raptor's settings in it), for another Panel.

## How eggs read

Every egg is written the same way, so the library reads as one thing.

**Names** are what players and owners call it, nothing else: `Valheim`,
`Node.js`, `Go`, `.NET`, `TeamSpeak 3`. Minecraft server software is
`Minecraft: <software>` (`Minecraft: Paper`). No "server", "dedicated",
"generic", or "egg", no version numbers (those are a variable), and no
trailing punctuation.

**Descriptions** are one to three plain sentences: what it is, then what
the egg sets up (`Runs a Node.js app from your files or a Git repository.`).
No links, no panel names, no marketing, no emoji. They end with a full
stop.

**Variables** have short names in sentence case (`Server name`, `Max
players`), an `UPPER_SNAKE_CASE` environment variable, a description that's
one sentence ending in a full stop, and rules that match what the game
accepts. Version variables are called `Version` and default to `latest`.

**Images** are Raptor's runtime images (`ghcr.io/xena-studios/yolks`) or,
where Raptor has none, pelican-eggs' (`ghcr.io/pelican-eggs/…`), always
pinned by digest: write the tag (`ghcr.io/xena-studios/yolks:java_21`) and
run `task library:pin`, which adds the digest it points to now. The
install container is `ghcr.io/xena-studios/yolks:installer`, pinned the
same way. Image names say the runtime and version (`Java 21`,
`Node.js 22`).

**The author** is `support@raptorpanel.net`.

## Minecraft Java eggs

`minecraft/paper` is the pattern for every Minecraft Java egg:

- **Images** are Raptor's Java images, newest first (`Java 25` … `Java 8`),
  so `latest` works out of the box.
- **The install** asks the software's own API for the version and build,
  refuses a version that doesn't exist (never falls back to the latest,
  which would upgrade the world), verifies the download against the
  published checksum, skips it when the jar already matches, and replaces
  the jar only once the new one checks out. It writes the Java version
  that Minecraft version needs to `.raptor-java`, and, after it, the
  newest Java it runs on where there is one (old Forge: `8 8`, Forge
  1.13–1.16.5: `8 11`, NeoForge 1.20.1: `17 21`).
- **The startup command** reads `.raptor-java` and refuses to start on a
  Java outside that range, saying which image to choose. It uses no command
  substitution (`$(…)`), so Wings can still check the jar. The heap is
  `-XX:MaxRAMPercentage=85.0` with PaperMC's recommended G1 flags and
  `-XX:+IgnoreUnrecognizedVMOptions`, so one command runs on Java 8 to 25.
- **Variables** keep upstream's environment names (`MINECRAFT_VERSION`,
  `BUILD_NUMBER`, `SERVER_JARFILE`), so a server on the old egg updates to
  the library's without losing its settings.

## Downloads from GitHub

Install scripts never call GitHub's API (`api.github.com`): its anonymous
limit, 60 requests an hour per address, runs out on nodes that share an
address and on CI runners. The installer image's `gh-release` reads the
same release pages a browser does:

```bash
tag=$(gh-release tag owner/repo "${VERSION:-latest}") || exit 1   # refuses a tag that doesn't exist
gh-release assets owner/repo "$tag"                              # "<file> <sha256>" per file
gh-release download owner/repo "$tag" app-linux-x64.tar.gz app.tar.gz   # checked against GitHub's SHA-256
```

`latest` is the newest full release (no pre-releases). Pick the file for
the node's CPU from `assets` by name (`uname -m`).

The install container's `/tmp` is a small in-memory folder: big
temporary files (a JDK to build with, archives) go in a folder under
`/mnt/server` such as `.raptor-build`, removed when the script exits.

## SteamCMD eggs

`steam/valheim` is the pattern for every SteamCMD game (`steam/rust` and
`steam/cs2` follow it with pelican-eggs' images):

- **The install** (on the installer image, as root) downloads SteamCMD
  into the server's `steamcmd/` folder once and keeps it there, so the
  server can update itself at start. It signs in **anonymously only**
  (a game that needs an account is left out of the library) and runs
  `app_update`, with `-beta`/`-betapassword` when set and `validate` only
  when Validate files is on. SteamCMD's exit code means nothing: success is
  its `Success! App '<id>'` line. Its transient failures (`Missing
  configuration`, state `0x202` or `0x602`, often right after it updates
  itself) are retried up to 5 times; `No subscription` (needs an account)
  and a refused beta stop at once, with one plain line. It then copies
  `steamclient.so` to `.steam/sdk32` and `.steam/sdk64` and ends with the
  installed build from `steamapps/appmanifest_<id>.acf`. `app_update`
  only adds and replaces the game's own files, so saves and configs stay;
  game-specific steps (mod loaders, config lines the Panel replaces, an
  opt-in wipe) come after, and anything destructive only once the game
  installed. `steam/cs2`'s Metamod:Source and CounterStrikeSharp
  are such steps, with the startup adding Metamod to `gameinfo.gi`
  ([docs/EGGS.md](../docs/EGGS.md#cs2-frameworks)). Large games set `"install": {"timeout": "4h"}` in the egg.
- **Images:** Raptor's `steamcmd` (Debian with SteamCMD's 32-bit
  libraries) when the game runs on it, named `SteamCMD`. Its entrypoint
  updates nothing, so the startup starts with the update step below and
  ends with `exec <game>`. pelican-eggs' images where the game needs
  something Raptor's lacks: `steamcmd:sniper` (`Steam Runtime 3`, Source 2
  games), a game image with a console wrapper (`games:rust`, which talks to
  Rust over RCON), or `wine`/`proton` for Windows-only servers (with
  `+@sSteamCmdForcePlatformType windows` in the install). Their
  entrypoints run the same update from the same variables and the same
  `steamcmd/` folder, so the startup is just the game.
- **Variables** keep upstream's names: `SRCDS_APPID` (`Steam app ID`,
  hidden, `in:<id>`), `AUTO_UPDATE` (`Auto update`, default 1),
  `VALIDATE` (`Validate files`, default 0), `SRCDS_BETAID` (`Beta
  branch`), `SRCDS_BETAPASS` (`Beta password`), and the game's own
  (`SERVER_NAME`, `MAX_PLAYERS`, `QUERY_PORT`, `RCON_PASS` …) as the old
  catalog's egg had them. Passwords and tokens have no default; one the
  console needs (Rust's RCON) is `required`, others `nullable`. Settings
  in a config file go through `config.files`, with the install adding the
  lines a `file` parser replaces.
- **Startup and stop:** the game's real done line; `stop` is the game's
  quit command (`quit`), or `^C` when it has no console, which reaches the
  game because the startup `exec`s it. A log filter runs in a process
  substitution that ignores SIGINT (`> >(trap '' INT; exec sed -u …)`), so
  the game's last lines still show while it saves.
  Unturned is the exception: Steam's game server sometimes fails to
  start, and the game then quits with exit code 0 before its done line
  (in CI, about one start in three, cause unknown). Its startup runs the
  game in a loop instead, retrying up to 3 times only when this start's
  `Logs/Server_<ID>.log` line says "Steam can not be initialized".
- **Raptor's settings:** `arch: [amd64]`. `players: {query: source}` for
  games that answer A2S, with `port` when that isn't the game port (`"+1"`,
  `QUERY_PORT`). `backup` when the console can save: `pre` the save command and
  `wait_for` its line, plus pause/resume (`post`) where the game has them. `workshop` when the game downloads Workshop items
  from a variable.
- **Tests:** `tier: slow` (a PR runs the eggs it changes; the whole tier
  runs weekly, amd64), `manual` with a reason for servers
  over about 14 GB, with the test variables a server needs to start
  (passwords) and a console `command`/`expect` where it has one.

The install's SteamCMD part, copied as it is (set `app`, then add the
game's own steps):

```bash
#!/bin/bash
# <Game>. Installs or updates the dedicated server (Steam app <id>) with
# SteamCMD, signed in anonymously. <What is never touched.>
set -euo pipefail
cd /mnt/server
export HOME=/mnt/server
app="${SRCDS_APPID:-<id>}"
fail() { echo "$1"; exit 1; }

# SteamCMD stays in the server folder, so the server can update itself at start.
if [ ! -x steamcmd/steamcmd.sh ]; then
    echo "Downloading SteamCMD"
    mkdir -p steamcmd
    curl -fsSL --retry 3 https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz | tar -xz -C steamcmd
fi
mkdir -p steamapps
args=(+force_install_dir /mnt/server +login anonymous +app_update "$app")
[ -z "${SRCDS_BETAID:-}" ] || args+=(-beta "$SRCDS_BETAID")
[ -z "${SRCDS_BETAPASS:-}" ] || args+=(-betapassword "$SRCDS_BETAPASS")
[ "${VALIDATE:-0}" != 1 ] || args+=(validate)
# SteamCMD often fails once for no lasting reason (Missing configuration,
# state 0x202 or 0x602 after it updates itself): try again, up to 5 times.
for try in 1 2 3 4 5; do
    ./steamcmd/steamcmd.sh "${args[@]}" +quit 2>&1 | tee /tmp/steamcmd.log || true
    grep -aq "Success! App '$app'" /tmp/steamcmd.log && break
    err=$(grep -aE '^(ERROR|Error)!' /tmp/steamcmd.log | tail -n 1) || true
    case "$err" in
    *"No subscription"*) fail "Steam won't give this game to an anonymous account, so it can't be installed." ;;
    *"Invalid Password"* | *"Invalid beta"*) fail "Steam refused the beta branch or its password. Check Beta branch and Beta password, then reinstall." ;;
    esac
    [ "$try" = 5 ] && fail "SteamCMD failed 5 times (${err:-no error given}). If it mentions 0x202, the server may be out of disk space. Reinstall to try again."
    echo "SteamCMD failed; trying again ($((try + 1)) of 5)."
    sleep 5
done
# Steam's client library, where the game looks for it.
mkdir -p .steam/sdk32 .steam/sdk64
cp -f steamcmd/linux32/steamclient.so .steam/sdk32/
cp -f steamcmd/linux64/steamclient.so .steam/sdk64/
build=$(sed -n 's/^[[:space:]]*"buildid"[[:space:]]*"\([0-9]*\)".*/\1/p' "steamapps/appmanifest_$app.acf" | head -n 1)


echo "Installed <Game> build ${build:-unknown}${SRCDS_BETAID:+ from the $SRCDS_BETAID branch}."
```

And the update step a startup on Raptor's `steamcmd` image begins with:

```bash
if [ "{{AUTO_UPDATE}}" = 1 ]; then ./steamcmd/steamcmd.sh +force_install_dir /home/container +login anonymous +app_update {{SRCDS_APPID}} ${SRCDS_BETAID:+-beta "$SRCDS_BETAID"} ${SRCDS_BETAPASS:+-betapassword "$SRCDS_BETAPASS"} $([ "{{VALIDATE}}" = 1 ] && echo validate) +quit || echo "Steam couldn't update the game, so the server starts as it is."; fi; exec ./<game> …
```
