#!/bin/bash
# Fills in install/get.sh.tmpl for a release: the version and the SHA-256 of
# each binary, from the release's (verified) checksums.txt.
#
#   scripts/make-install-script.sh v1.2.3 checksums.txt > install.sh
set -euo pipefail

tag=${1:?usage: make-install-script.sh <tag> <checksums.txt>}
sums=${2:?usage: make-install-script.sh <tag> <checksums.txt>}
here=$(cd "$(dirname "$0")/.." && pwd)

sha() {
	local s
	s=$(awk -v f="raptor_linux_$1" '$2 == f || $2 == "*"f {print $1}' "$sums")
	[[ $s =~ ^[0-9a-f]{64}$ ]] || { echo "no SHA-256 for raptor_linux_$1 in $sums" >&2; exit 1; }
	echo "$s"
}
amd64=$(sha amd64)
arm64=$(sha arm64)
sed -e "s/__VERSION__/${tag#v}/g" -e "s/__TAG__/$tag/g" \
	-e "s/__SHA256_AMD64__/$amd64/" -e "s/__SHA256_ARM64__/$arm64/" "$here/install/get.sh.tmpl"
