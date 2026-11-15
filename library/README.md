# Egg library

Eggs for the games and apps people host, made by Raptor and imported into
a Panel's official egg library by hand: none of them is built into the
Panel, whose catalog (`eggs/`) has only the test server. See
[docs/EGGS.md](../docs/EGGS.md#egg-library).

One folder per egg, `<category>/<egg>/`, in the catalog's layout:

- `egg-<egg>.json`: the egg, in Raptor's own format (`RPTR_v1`,
  [docs/EGGS.md](../docs/EGGS.md#raptor-eggs)).
- `raptor.yaml`: `source` (this repository, the egg's path, no commit),
  `license: MIT`, `credits` (what it's based on, with the licence), `arch`,
  Raptor's settings (`players`, `addons`, `workshop`, `backup`), and its
  conformance `test`.
- `icon.png`: the game's own icon, square, 256×256. Where it came from is
  in `raptor.yaml`'s `credits`.

**The folder is the egg's ID** (`minecraft/paper`), and it never changes:
importing the library into the official library again adds a version to
the egg with that ID instead of a second egg, and an egg whose ID a
retired catalog egg had (one servers still use) becomes that egg's newest
version. Keep the old catalog's IDs for the games it had.

`go test ./library` checks every egg against the rules below.
`task library:export` writes one file per egg to import (the egg with its
icon and Raptor's settings in it), and
`task e2e:conformance SOURCE=library RUN=<id>` runs one through Wings.

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
  that Minecraft version needs to `.raptor-java`.
- **The startup command** reads `.raptor-java` and refuses to start on an
  older Java, saying which image to choose. It uses no command
  substitution (`$(…)`), so Wings can still check the jar. The heap is
  `-XX:MaxRAMPercentage=85.0` with PaperMC's recommended G1 flags and
  `-XX:+IgnoreUnrecognizedVMOptions`, so one command runs on Java 8 to 25.
- **Variables** keep upstream's environment names (`MINECRAFT_VERSION`,
  `BUILD_NUMBER`, `SERVER_JARFILE`), so a server on the old egg updates to
  the library's without losing its settings.
