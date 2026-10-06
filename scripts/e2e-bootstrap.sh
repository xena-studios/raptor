#!/bin/bash
# The install script and raptor bootstrap on a fresh VM (task e2e:bootstrap):
# the script, piped to sudo bash as owners run it, takes a bare box to a
# linked node, running it again changes nothing, doctor
# passes, and after a reboot the volume is mounted and the node reconnects.
# Needs the dev Panel on this machine (task dev, or panel serve api on :8080).
#
#   scripts/e2e-bootstrap.sh <vm name> <lima template: debian-12, debian-13, ubuntu-24.04>
set -euo pipefail

VM=$1
TEMPLATE=${2:-debian-12}
PANEL=http://host.lima.internal:8080
DB=${DEV_DATABASE_URL:-postgres://raptor:raptor@127.0.0.1:54320/raptor}

pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; exit 1; }
status() { limactl shell "$VM" sudo raptor status 2>&1; }
connected() { status | grep -q '✓ connected'; }
wait_for() { # seconds, command…
	local n=$1
	shift
	for _ in $(seq 1 "$n"); do if "$@"; then return 0; fi; sleep 1; done
	return 1
}

curl -fsS -o /dev/null http://127.0.0.1:8080/healthz || fail "the dev Panel isn't running on :8080 (task dev)"
[ -s .dev/org ] || PANEL_DATABASE_URL=$DB go run ./cmd/panel org create "Dev org" >.dev/org
token() { PANEL_DATABASE_URL=$DB go run ./cmd/panel join-token "$(cat .dev/org)" "e2e-$TEMPLATE"; }

if ! limactl list -q | grep -qx "$VM"; then
	limactl start --name="$VM" --tty=false --mount-none "template://$TEMPLATE" >/dev/null
fi
# A release as the install script sees it: both binaries, their checksums,
# the generated script, served from this machine.
TAG=v0.0.0-e2e
REL=$(mktemp -d)
trap 'rm -rf "$REL"; [ -n "${SERVER:-}" ] && kill "$SERVER" 2>/dev/null' EXIT
for a in amd64 arm64; do
	CGO_ENABLED=0 GOOS=linux GOARCH=$a go build -trimpath \
		-ldflags "-X github.com/xena-studios/raptor/internal/shared/buildinfo.Version=${TAG#v}" \
		-o "$REL/raptor_linux_$a" ./cmd/raptor
done
(cd "$REL" && shasum -a 256 raptor_linux_* >checksums.txt)
scripts/make-install-script.sh "$TAG" "$REL/checksums.txt" >"$REL/install.sh"
PORT=$((20000 + RANDOM % 10000))
python3 -m http.server "$PORT" --bind 0.0.0.0 --directory "$REL" >/dev/null 2>&1 &
SERVER=$!
sleep 1
install_sh() { limactl shell "$VM" sudo env RAPTOR_DOWNLOAD_BASE="http://host.lima.internal:$PORT" bash -s -- "$@" <"$REL/install.sh"; }

# A tampered binary is refused before anything runs.
for a in amd64 arm64; do cp "$REL/raptor_linux_$a" "$REL/good_$a" && echo tampered >>"$REL/raptor_linux_$a"; done
if install_sh -token x >/tmp/e2e-tampered.log 2>&1; then
	fail "a tampered binary was installed"
fi
grep -q "doesn't match" /tmp/e2e-tampered.log || fail "tampered: $(cat /tmp/e2e-tampered.log)"
limactl shell "$VM" test ! -e /usr/local/bin/raptor || fail "something was installed from the tampered download"
for a in amd64 arm64; do mv "$REL/good_$a" "$REL/raptor_linux_$a"; done
pass "install script: a tampered download is refused"

install_sh -token "$(token)" -panel "$PANEL" -yes -size 4GiB || fail "install script and bootstrap"
wait_for 30 connected || fail "not connected after bootstrap: $(status)"
limactl shell "$VM" readlink /usr/local/lib/raptor/current | grep -qx "raptor-${TAG#v}" || fail "layout: $(limactl shell "$VM" ls -l /usr/local/lib/raptor)"
pass "install script + bootstrap: a fresh $TEMPLATE box linked and connected"

started=$(limactl shell "$VM" systemctl show -p ActiveEnterTimestampMonotonic --value raptor-wings)
limactl shell "$VM" sudo raptor bootstrap -token unused -yes >/tmp/e2e-bootstrap-rerun.log 2>&1 || fail "rerun: $(cat /tmp/e2e-bootstrap-rerun.log)"
grep -q "already linked" /tmp/e2e-bootstrap-rerun.log || fail "rerun didn't see the link"
[ "$(limactl shell "$VM" systemctl show -p ActiveEnterTimestampMonotonic --value raptor-wings)" = "$started" ] || fail "rerun restarted Wings"
pass "rerun: nothing changed, Wings not restarted"

limactl shell "$VM" sudo raptor doctor >/tmp/e2e-bootstrap-doctor.log 2>&1 || fail "doctor: $(cat /tmp/e2e-bootstrap-doctor.log)"
pass "doctor: $(tail -1 /tmp/e2e-bootstrap-doctor.log)"

limactl stop "$VM" >/dev/null 2>&1
limactl start "$VM" --tty=false >/dev/null 2>&1
wait_for 60 connected || fail "not connected after a reboot: $(status)"
status | grep -q 'Storage  ✓' || fail "volume after a reboot: $(status)"
pass "reboot: volume mounted, node reconnected"
echo "ALL PASSED ($TEMPLATE). Delete the VM with: limactl delete -f $VM"
