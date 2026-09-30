#!/usr/bin/env bash
# Builds what scripts/e2e-update.sh needs into a directory: e2eupdate builds of
# Wings (internal/wings/update/e2e.go) and a release server tree like GitHub's,
# signed with a throwaway key. Usage: scripts/e2e-update-build.sh <arch> <dir>
#   srv/          releases: 1.0.0 (stable), 1.0.1 (beta), 1.1.0 crashes on
#                 start, 1.2.0 never becomes healthy
#   key.pub       the throwaway release key
#   raptor-0.9.0  the version installed first
set -euo pipefail

arch=${1:?usage: e2e-update-build.sh <arch> <dir>}
out=${2:?usage: e2e-update-build.sh <arch> <dir>}
command -v minisign >/dev/null || { echo "minisign not installed" >&2; exit 1; }

rm -rf "$out" && mkdir -p "$out"
minisign -G -W -f -p "$out/key.pub" -s "$out/key.sec" >/dev/null

build() { # version, break mode, output
	CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -tags e2eupdate -o "$3" -ldflags "
		-X github.com/xena-studios/raptor/internal/shared/buildinfo.Version=$1
		-X github.com/xena-studios/raptor/internal/wings/update.e2eBreak=$2" ./cmd/raptor
}

build 0.9.0 "" "$out/raptor-0.9.0"
for v in 1.0.0: 1.0.1: 1.1.0:crash 1.2.0:hang; do
	ver=${v%%:*}
	d=$out/srv/xena-studios/raptor/releases/download/v$ver
	mkdir -p "$d"
	build "$ver" "${v#*:}" "$d/raptor_linux_$arch"
	(cd "$d" && sha256sum "raptor_linux_$arch" 2>/dev/null >checksums.txt || shasum -a 256 "raptor_linux_$arch" >checksums.txt)
	minisign -S -s "$out/key.sec" -m "$d/checksums.txt" -t "raptor v$ver checksums.txt" >/dev/null
done
mkdir -p "$out/srv/repos/xena-studios/raptor"
echo '[{"tag_name":"v1.2.0","prerelease":true},{"tag_name":"v1.1.0","prerelease":true},{"tag_name":"v1.0.1","prerelease":true},{"tag_name":"v1.0.0"}]' \
	>"$out/srv/repos/xena-studios/raptor/releases"
rm "$out/key.sec"
