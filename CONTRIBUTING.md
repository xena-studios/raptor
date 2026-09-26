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
task test      # all tests, including Postgres-backed ones
task gen       # regenerate protobuf, sqlc, and route tree code
task lint      # golangci-lint, buf lint, Biome
task fmt       # format Go and web code
task check     # everything CI runs: lint, generated code, tests, licenses, vulns, secrets
task build     # binaries into bin/, web app into web/dist
task --list    # everything else
```

Generated code is committed. After changing anything in `proto/`, `db/`, or `web/src/routes/`, run `task gen` and commit the result.

### Running Wings

Wings only runs on Linux. For development it runs in a Debian 12 VM:

```bash
task wings:vm:up        # create/start the VM (first run downloads the image)
task wings:vm:deploy    # build raptor for the VM and copy it to /usr/local/bin
task wings:vm:install   # deploy + systemd unit + dev config, then (re)start Wings
task wings:vm:shell     # shell into the VM (then: sudo raptor status)
```

### Egg end-to-end tests

Eggs are tested by installing and running them with their real, unmodified images:

```bash
task e2e:eggs                      # Paper (both formats) + Node.js in the VM
task e2e:eggs RUN=TestEggNode      # one egg
```

x86-only eggs like Rust can't run on an ARM Mac. They run in the **E2E eggs** GitHub workflow on an x86 runner: start it from the Actions tab, or push a branch named `e2e/<anything>`.

### Fuzz tests

The egg engine handles untrusted input (egg files, variable values, config files on disk), so its parsers are fuzzed:

```bash
task fuzz                  # every fuzz test, 30s each
task fuzz FUZZTIME=5m      # longer
```

When the fuzzer finds a failing input, it saves it under `testdata/fuzz/`. Fix the bug and commit that file: it then runs as a regular test on every `go test`.

### Runtime end-to-end tests

The container runtime (networks, firewall isolation, resource limits, hardening, port checks), the server lifecycle (install, power actions, console, crashes, reconcile, resumed installs, events), and the command path (Panel grants, passkey-signed commands) are tested against a real Docker:

```bash
task e2e:runtime                          # all runtime and lifecycle tests in the VM
task e2e:runtime RUN=TestCrashPolicy      # one test
task e2e:host                             # Wings/Docker restarts, host shutdown, and a real reboot
```

`e2e:host` installs Wings with its systemd units in the VM, seeds a running server, and reboots the VM, so it takes a few minutes.

They need root: they create Wings' networks, load its nftables table, and set up `raptor.slice`, exactly as the daemon does. CI runs them on every PR (the `e2e-runtime` job).

### Releases

Releases are built as drafts by CI when a `v*` tag is pushed, then signed with the offline minisign key and published by a maintainer. See [release/README.md](release/README.md).

## Pull requests

`main` is protected. Every change lands through a pull request, and all CI jobs (`lint`, `generated`, `test`, `build`, `security`, `e2e-runtime`, `pr`) must pass. Merges are **squash only**, so every commit on `main` is signed by GitHub, and `main` **requires signed commits**. Sign your own commits too (GPG or SSH signing). Use conventional commit titles (`feat:`, `fix:`, `docs:`, …).

## Ground rules

- **Security issues:** don't open a public issue. See [SECURITY.md](SECURITY.md).
- **Dependencies** must be AGPL-3.0-compatible (MIT, BSD, Apache-2.0, MPL-2.0, LGPL, GPL/AGPL). No SSPL, BSL, "Commons Clause", or proprietary licenses. CI enforces this.
- **No secrets in the repo.** CI runs secret scanning.
- Large changes should start as an issue or discussion so the design can be agreed before code is written.
