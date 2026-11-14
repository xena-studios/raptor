#!/bin/bash
# Smoke-tests one of Raptor's runtime images the way Wings runs servers:
# as an unknown user (no such account in the image), with a read-only root
# filesystem, a tmpfs /tmp, every capability dropped, and STARTUP with
# {{VAR}} placeholders. Usage: test.sh <image> <tag> (tag from images.json).
set -euo pipefail
img=$1 tag=$2
here=$(cd "$(dirname "$0")" && pwd)
cmd=$(jq -r --arg t "$tag" '.images[] | select(.tag == $t) | .test' "$here/images.json")
expect=$(jq -r --arg t "$tag" '.images[] | select(.tag == $t) | .expect' "$here/images.json")
dir=$(mktemp -d)
chmod 0777 "$dir"
# The server writes as UID 987, which the runner can't delete as itself.
trap 'docker run --rm -v "$dir:/d" --entrypoint /bin/sh "$img" -c "rm -rf /d/* /d/.[!.]*" >/dev/null 2>&1; rm -rf "$dir"' EXIT
fail() { echo "FAIL $tag: $*" >&2; echo "$out" >&2; exit 1; }

if [ "$tag" = installer ]; then
	out=$(docker run --rm -v "$dir:/mnt/server" "$img" bash -euc \
		'for t in curl wget jq unzip zip tar xz zstd bzip2 git file; do command -v $t >/dev/null || { echo "missing $t"; exit 1; }; done; echo tools-ok > /mnt/server/ok' 2>&1) || fail "the install tools"
	[ "$(cat "$dir/ok")" = tools-ok ] || fail "couldn't write /mnt/server"
	echo "ok $tag"
	exit 0
fi

# shellcheck disable=SC2016 # expanded in the container, not here
startup='echo "var={{FOO}} port={{SERVER_PORT}} home=$HOME pwd=$(pwd)" && touch /home/container/written && touch /tmp/scratch'
[ -n "$cmd" ] && startup="$startup && $cmd"
out=$(docker run --rm --read-only --tmpfs /tmp:rw,exec --user 987:987 --cap-drop ALL --security-opt no-new-privileges \
	-v "$dir:/home/container" -e TZ=Europe/London -e FOO=bar -e SERVER_PORT=25565 -e STARTUP="$startup" "$img" 2>&1) || fail "the startup command failed"
echo "$out" | grep -q 'var=bar port=25565 home=/home/container pwd=/home/container' || fail "variables, HOME, or the folder"
[ -f "$dir/written" ] || fail "couldn't write to /home/container"
[ -z "$expect" ] || echo "$out" | grep -qF -- "$expect" || fail "expected $expect"
echo "ok $tag"
