#!/usr/bin/env bash
# Signs a draft GitHub release with the offline minisign key, verifies it, and publishes it.
# The private key never touches CI. Usage: scripts/sign-release.sh v1.2.3
set -euo pipefail

tag=${1:?usage: sign-release.sh <tag>}
key=${MINISIGN_KEY:-$HOME/.minisign/raptor.key}
pub=release/minisign.pub
repo=xena-studios/raptor

command -v minisign >/dev/null || { echo "minisign not installed (brew install minisign)"; exit 1; }
[ -f "$pub" ] || { echo "missing $pub"; exit 1; }
[ -f "$key" ] || { echo "missing private key at $key (set MINISIGN_KEY)"; exit 1; }

draft=$(gh release view "$tag" -R "$repo" --json isDraft -q .isDraft)
[ "$draft" = "true" ] || { echo "$tag is not a draft release"; exit 1; }

dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT

gh release download "$tag" -R "$repo" -D "$dir"
(cd "$dir" && sha256sum -c checksums.txt 2>/dev/null || shasum -a 256 -c checksums.txt)

minisign -S -s "$key" -m "$dir/checksums.txt" -t "raptor $tag checksums.txt"
minisign -V -p "$pub" -m "$dir/checksums.txt"

gh release upload "$tag" -R "$repo" "$dir/checksums.txt.minisig"

read -r -p "Publish $tag? [y/N] " ok
[ "$ok" = "y" ] && gh release edit "$tag" -R "$repo" --draft=false && echo "published $tag"
