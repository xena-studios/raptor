# Raptor's runtime images

`ghcr.io/xena-studios/yolks:<tag>`: the images the egg library's eggs run
in (docs/EGGS.md#egg-library). Small, multi-arch where the runtime is,
rebuilt every week with the base images' security updates, and smoke-tested
the way Wings runs servers before anything is published.

| Tag | Base | CPUs |
|---|---|---|
| `java_8`, `java_11`, `java_17`, `java_21`, `java_25` | Eclipse Temurin JRE (Ubuntu) | amd64, arm64 |
| `nodejs_22`, `nodejs_24`, `nodejs_26` | Node.js (Debian slim) | amd64, arm64 |
| `python_3.11` … `python_3.15` | Python (Debian slim) | amd64, arm64 |
| `dotnet_8`, `dotnet_10` | .NET SDK, with the ASP.NET Core runtime (Ubuntu) | amd64, arm64 |
| `bun_1`, `deno_2` | Bun, Deno (Debian) | amd64, arm64 |
| `go_1.26`, `go_1.27`, `rust_1` | Go, Rust toolchains (Debian) | amd64, arm64 |
| `steamcmd` | Debian slim with the 32-bit libraries SteamCMD and Source games need | amd64 |
| `installer` | Debian slim with what install scripts use (curl, jq, unzip, git, …) | amd64, arm64 |

Only supported releases: Java's long-term ones, Node's LTS and current,
Python's maintained versions, .NET's LTS. A version leaves when its
upstream support ends.

## What every image does

- **Works the way Wings runs servers:** as the servers' user (no account
  in the image is needed), with a read-only root filesystem and a tmpfs
  `/tmp`. `HOME` is `/home/container`, the server's folder; nothing is
  written anywhere else, so caches point there too (`GOPATH`,
  `CARGO_HOME`, `DENO_DIR`, NuGet's in `HOME`).
- **Starts eggs unchanged:** the entrypoint (`entrypoint.sh`) behaves like
  the Pterodactyl and Pelican yolks': it sets `TZ` and `INTERNAL_IP`, turns
  `{{VAR}}` in `STARTUP` into `${VAR}`, and evaluates it in
  `/home/container`, under `tini` so signals reach the server.
- **Has nothing to install at start:** curl, git, jq, and the runtime are
  in the image. The installer image has what install scripts use, so no
  script runs `apt-get`.

## Changing them

`images.json` lists every tag: its Dockerfile's folder, the `VERSION` it's
built with, its CPUs, and the command and output its smoke test checks.
`test.sh <image> <tag>` runs the smoke test locally. The **Yolks** workflow
builds every image natively on each CPU for a pull request (publishing
nothing), and publishes on `main`, weekly, and on demand. Eggs pin images
by digest, so a rebuilt image reaches servers only when an egg is updated.
