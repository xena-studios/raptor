# Release signing

`minisign.pub` is the public key that verifies Raptor releases. Each release's
`checksums.txt` is signed (`checksums.txt.minisig`); the checksums cover every binary.

The private key is kept **offline** by the maintainer and is never stored in the
repository or in CI. Releases are built as drafts by CI and signed locally:

```bash
git tag v0.1.0 && git push origin v0.1.0   # CI builds a draft release
task release:sign TAG=v0.1.0                 # sign, verify, upload signature, publish
```

## Verifying a release

```bash
minisign -V -p minisign.pub -m checksums.txt   # signature is valid
sha256sum -c checksums.txt                     # binaries match
```
