# Contributing to Raptor

Thanks for your interest in Raptor. The project is in the design phase, so the most useful contributions right now are feedback on the documents in [`docs/`](docs/).

## Developer Certificate of Origin (DCO)

Raptor uses the [Developer Certificate of Origin 1.1](https://developercertificate.org/) instead of a CLA. You keep the copyright to your contributions, and they are licensed under the project license (AGPL-3.0).

Every commit must be signed off:

```bash
git commit -s -m "fix console reconnect after node connection drop"
```

This adds a line to the commit message:

```
Signed-off-by: Your Name <you@example.com>
```

By adding it, you certify that you wrote the change (or otherwise have the right to submit it) and that you're contributing it under the project's license, as described in the DCO. The name and email must be real and must match the commit author. A CI check blocks PRs that contain unsigned commits.

Forgot to sign off? Fix the last commit with `git commit --amend -s`, or a whole branch with `git rebase --signoff main`.

## Development

### Requirements

- Go (version in `go.mod`), Node 24+, pnpm
- Docker (Docker Desktop or OrbStack)
- [Task](https://taskfile.dev) and [Lima](https://lima-vm.io): `brew install go-task lima`

Dev tools (buf, sqlc, protoc plugins, golangci-lint, govulncheck, go-licenses) are pinned in `tools/go.mod` and run through `go tool`, so there's nothing else to install. gitleaks runs from its Docker image.

### Common tasks

```bash
task setup     # install web deps, start Postgres
task dev       # Postgres + Panel API (:8080) + web dev server (:5173)
task dev:link  # link the dev VM's Wings to that Panel (a dev org and join token)
task test      # all tests, including Postgres-backed ones
task gen       # regenerate protobuf, sqlc, and route tree code
task lint      # golangci-lint, buf lint, Biome
task fmt       # format Go and web code
task check     # everything CI runs: lint, generated code, tests, licenses, vulns, secrets
task build     # binaries into bin/, web app into web/dist
task --list    # everything else
```

To sign in to the dev Panel, ask for a code in the web app: `task dev` writes sign-in emails to the API's log (`PANEL_MAIL_LOG=1`), code and link included. The dev Panel's signing key, data key (for TOTP), and org live in `.dev/` (ignored by git). After `task dev:link`, `raptor status` in the VM shows the connection; `task wings:vm:install` puts the dev config back, which unlinks the VM.

Generated code is committed. After changing anything in `proto/`, `db/`, or `web/src/routes/`, run `task gen` and commit the result.

### Running Wings

Wings only runs on Linux. For development it runs in a Debian 12 VM:

```bash
task wings:vm:up        # create/start the VM (first run downloads the image)
task wings:vm:deploy    # build raptor for the VM and install it as /usr/local/lib/raptor/raptor-dev
task wings:vm:install   # deploy + systemd unit + dev config, then (re)start Wings
task wings:vm:shell     # shell into the VM (then: sudo raptor status)
```

### Egg conformance tests

Catalog eggs (`eggs/`) are tested by installing and running them with their real, unmodified images through the full Wings lifecycle (install, start, console command, Wings restart, stop, reinstall):

```bash
task e2e:conformance                             # the fast tier in the VM
task e2e:conformance RUN=minecraft/paper         # one egg
task e2e:conformance TIER=slow RUN=steam/rust    # x86-only: not on an ARM Mac
```

`task e2e:pterodactyl` compares Raptor with the real Pterodactyl Panel and Wings in the VM (`scripts/pterodactyl/setup.sh` starts them; `setup.sh down` removes them). See [EGGS.md](docs/EGGS.md#behavioral-diff-against-pterodactyl).

The **Egg conformance** GitHub workflow runs the fast tier on amd64 and arm64 for PRs that touch eggs or the code that runs them, and nightly; the slow tier (big SteamCMD games) runs weekly. Start either from the Actions tab, or push a branch named `conformance/<anything>` to run both. To add an egg, see [EGGS.md](docs/EGGS.md#built-in-catalog).

### Fuzz tests

The egg engine handles untrusted input (egg files, variable values, config files on disk), so its parsers are fuzzed, as are the command envelope and cron expressions:

```bash
task fuzz                  # every fuzz test, 30s each
task fuzz FUZZTIME=5m      # longer
```

When the fuzzer finds a failing input, it saves it under `testdata/fuzz/`. Fix the bug and commit that file: it then runs as a regular test on every `go test`.

### Runtime end-to-end tests

The container runtime (networks, firewall isolation, resource limits, hardening, port checks), the server lifecycle (install, power actions, console, crashes, reconcile, resumed installs, events), the command path (Panel grants, passkey-signed commands), schedules (a scheduled console command and restart), and backups (the egg's hooks, a signed restore, the worker's systemd scope, and an S3 destination on MinIO, pulled from `cgr.dev/chainguard/minio`) are tested against a real Docker:

```bash
task e2e:runtime                          # all runtime and lifecycle tests in the VM
task e2e:runtime RUN=TestCrashPolicy      # one test
task e2e:host                             # Wings/Docker restarts, host shutdown, the CLI, and a real reboot
task e2e:quotas                           # disk quotas on a real XFS loop volume, with a reboot
task e2e:update                           # self-update: an update, two rollbacks, a restart mid-trial (needs minisign)
```

`e2e:runtime` includes `TestPathSafety`, a hostile egg that plants symlinks, FIFOs, and links to host files (and then goes after them over SFTP, the web file manager's operations, and archives too) (see [SECURITY-MODEL.md](docs/SECURITY-MODEL.md#server-files)); if you change how Wings touches server files, break the protection on purpose once and check that it fails. Do that in a throwaway VM (`VM=<name>`), never the shared one: a broken build really does write through the planted links, and has emptied `/etc/passwd` (breaking `sudo`) and chowned `/etc` and `/usr` in the VM. The suite also checks the owners of the directories the links point at. `e2e:host` installs Wings with its systemd units and a quota volume in the VM, seeds a running server, runs the CLI against it (as root and as a `raptor` group member, including a backup and restore through the daemon's real worker), and reboots the VM, so it takes a few minutes. `e2e:quotas` creates a loop volume under `/var/lib/raptor-gate` with Wings' own code, checks limits on the host and in containers (including the seccomp escape check) and in the server manager, reboots, and grows the volume online. `e2e:update` builds signed test releases with a throwaway key (`scripts/e2e-update-build.sh`; the `e2eupdate` build tag lets those binaries take the key, a local release server, and shorter trial timings from the environment, and build versions that crash or hang on purpose), then updates Wings through the real systemd unit with a server running. It takes about 3 minutes and leaves the VM on the dev build. `e2e:faults` creates its own VM (`raptor-faults`, or `FAULTS_VM`) and injects faults: it SIGKILLs Wings mid-backup and Docker, fills the host disk and a server's quota, unmounts the data volume, and corrupts `state.db`, checking after each that Wings did what [RELIABILITY.md](docs/RELIABILITY.md) promises. It's destructive by design: never point it at the shared VM. Delete its VM afterwards with `task VM=raptor-faults wings:vm:delete`. `soak:start` sets up the soak test in its own VM (`raptor-soak`, or `SOAK_VM`) and leaves it running: check it with `soak:report`, end it with `soak:stop`. For a quick check of the harness itself, make everything fast: `task SOAK_RESTART='*/4 * * * *' SOAK_BACKUP='*/3 * * * *' SOAK_CHAOS_MIN=60 SOAK_CHAOS_MAX=150 soak:start`. Pass `VM=<name>` to any VM task to use a separate VM.

`task e2e:bootstrap` (with `task dev` running) creates a fresh VM from Lima's own template (`DISTRO=debian-12`, `debian-13`, or `ubuntu-24.04`; `BOOT_VM` names it), builds a test release with its install script, serves it from your machine, and pipes the script to `sudo bash` in the VM as an owner would, against the dev Panel: a tampered binary must be refused, then one command to a linked node, a rerun that must change nothing (Wings isn't even restarted), `raptor doctor`, and a reboot after which the volume must be mounted and the node connected. Delete the VM afterwards (`limactl delete -f raptor-boot`).

`task gate:link` runs the node connection through Cloudflare (`dev/linkgate`): the real Panel and Wings connection code on your machine, with a Cloudflare quick tunnel between them (`brew install cloudflared`; no account needed). It checks calls both ways, a connection cut mid-command (the command must run once), and console latency on the main connection while a 1 GB upload runs on its transfer connection; `ARGS='-idle 4h'` then holds the connection idle and logs every drop. The upload goes up your uplink and back down, so its speed is your upload speed. `-public-url` uses a URL you've already routed to `-listen` instead of a quick tunnel.

They need root: they create Wings' networks, load its nftables table, and set up `raptor.slice`, exactly as the daemon does. CI runs them on every PR (the `e2e-runtime` job).

### Releases

Releases are built as drafts by CI when a `v*` tag is pushed, then signed with the offline minisign key and published by a maintainer. See [release/README.md](release/README.md).

## Pull requests

`main` is protected. Every change lands through a pull request, and all CI jobs (`lint`, `generated`, `test`, `build`, `security`, `e2e-runtime`, `e2e-update`, `pr`) must pass. Merges are **squash only**, so every commit on `main` is signed by GitHub, and `main` **requires signed commits**. Sign your own commits too (GPG or SSH signing). Use conventional commit titles (`feat:`, `fix:`, `docs:`, …).

## Ground rules

- **Security issues:** don't open a public issue. See [SECURITY.md](SECURITY.md).
- **Dependencies** must be AGPL-3.0-compatible (MIT, BSD, ISC, Apache-2.0, MPL-2.0, CC0, LGPL, GPL/AGPL). No SSPL, BSL, "Commons Clause", or proprietary licenses. CI enforces this.
- **No secrets in the repo.** CI runs secret scanning.
- Large changes should start as an issue or discussion so the design can be agreed before code is written.
