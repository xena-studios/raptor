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

## Ground rules

- **Security issues:** don't open a public issue. See [SECURITY.md](SECURITY.md).
- **Dependencies** must be AGPL-3.0-compatible (MIT, BSD, Apache-2.0, MPL-2.0, LGPL, GPL/AGPL). No SSPL, BSL, "Commons Clause", or proprietary licenses. CI enforces this.
- **No secrets in the repo.** CI runs secret scanning.
- Large changes should start as an issue or discussion so the design can be agreed before code is written.
