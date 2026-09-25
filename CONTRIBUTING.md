# Contributing to Raptor

Thanks for your interest in Raptor. The project is in the design phase, so the most useful contributions right now are feedback on the documents in [`docs/`](docs/).

## Developer Certificate of Origin (DCO)

Raptor uses the [Developer Certificate of Origin 1.1](https://developercertificate.org/) instead of a CLA. You keep the copyright to your contributions, and they are licensed under the project license (AGPL-3.0).

Every commit must be signed off:

```bash
git commit -s -m "fix console reconnect on tunnel drop"
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
task wings:vm:up       # create/start the VM (first run downloads the image)
task wings:vm:deploy   # build raptor for the VM and install it
task wings:vm:shell    # shell into the VM
```

### Releases

Releases are built as drafts by CI when a `v*` tag is pushed, then signed with the offline minisign key and published by a maintainer. See [release/README.md](release/README.md).

## Pull requests

`main` is protected. Every change lands through a pull request, and all CI jobs (`lint`, `generated`, `test`, `build`, `security`, `pr`) must pass. Merges are squash or rebase only (linear history). Use conventional commit titles (`feat:`, `fix:`, `docs:`, …).

## Ground rules

- **Security issues:** don't open a public issue. See [SECURITY.md](SECURITY.md).
- **Dependencies** must be AGPL-3.0-compatible (MIT, BSD, Apache-2.0, MPL-2.0, LGPL, GPL/AGPL). No SSPL, BSL, "Commons Clause", or proprietary licenses. CI enforces this.
- **No secrets in the repo.** CI runs secret scanning.
- Large changes should start as an issue or discussion so the design can be agreed before code is written.
