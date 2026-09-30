#!/usr/bin/env bash
# Self-update end to end, on a node with raptor-wings installed, as root (task
# e2e:update in the VM, and the e2e-update CI job). The first argument is the
# directory scripts/e2e-update-build.sh made: releases, their key, and the
# version installed first. The second is the server package's e2e test binary,
# which seeds a running game server that every update must leave alone.
set -euo pipefail

rel=$1
lib=/usr/local/lib/raptor
work=/var/lib/raptor-e2e/update
dropin=/etc/systemd/system/raptor-wings.service.d/e2e-update.conf
export RAPTOR_E2E_TRIAL_TIMEOUT=45s # the CLI waits for the trial too

rm -rf "$work" && mkdir -p "$work"
# Updates prune other versions; the dev build is put back at the end.
cp "$(readlink -f "$lib/current")" "$work/raptor-dev"

step() { echo; echo "== $*"; }
fail() {
	echo "FAIL: $*" >&2
	journalctl -u raptor-wings -n 80 --no-pager >&2 || true
	exit 1
}
mainexe() { readlink "/proc/$(systemctl show -p MainPID --value raptor-wings)/exe"; }
current() { readlink "$lib/current"; }
# Waits for Wings to answer with the runtime ready, running version $1.
wait_up() {
	for _ in $(seq 60); do
		if s=$(raptor status 2>/dev/null) && grep -q "^Wings    $1 " <<<"$s" && grep -q "^Servers  [0-9]" <<<"$s"; then
			return 0
		fi
		sleep 1
	done
	fail "Wings $1 didn't come up"
}
# The seeded server's container: its start time must never change.
server() { docker inspect -f '{{.State.Running}} {{.State.StartedAt}}' "raptor-$ID"; }

python3 -m http.server 8099 --bind 127.0.0.1 --directory "$rel/srv" >/dev/null 2>&1 &
server=$!
cleanup() {
	kill "$server" 2>/dev/null || true
	rm -f "$dropin"
	systemctl daemon-reload
	# Back to the dev build.
	install -m 0755 "$work/raptor-dev" "$lib/raptor-dev"
	ln -sfn raptor-dev "$lib/current"
	rm -f /var/lib/raptor/update.json
	systemctl restart raptor-wings
}
trap cleanup EXIT

mkdir -p "$(dirname "$dropin")"
cat >"$dropin" <<EOF
[Service]
Environment=RAPTOR_E2E_RELEASE_KEY=$(tail -n 1 "$rel/key.pub")
Environment=RAPTOR_E2E_RELEASE_URL=http://127.0.0.1:8099
Environment=RAPTOR_E2E_TRIAL_TIMEOUT=$RAPTOR_E2E_TRIAL_TIMEOUT
Environment=RAPTOR_E2E_SETTLE=15s
EOF

step "install 0.9.0 with a running server"
systemctl daemon-reload
systemctl stop raptor-wings
ID=$(RAPTOR_E2E_SEED_NAME=update-test-$(date +%s) RAPTOR_E2E_SEED_DB=/var/lib/raptor/state.db "$2" -test.run '^TestSeedRealDaemon$' | awk '/^SEEDED/ {print $2}')
[ -n "$ID" ] || fail "seeding a server"
install -m 0755 "$rel/raptor-0.9.0" "$lib/raptor-0.9.0"
ln -sfn raptor-0.9.0 "$lib/current"
rm -f /var/lib/raptor/update.json
systemctl start raptor-wings
wait_up 0.9.0
before=$(server)
[[ $before == true* ]] || fail "server isn't running: $before"

step "check"
out=$(raptor update -check)
echo "$out"
grep -q "1.0.0 is available (stable channel)" <<<"$out" || fail "check: $out"
# Only root installs.
if out=$(runuser -u raptor -- raptor update 2>&1); then fail "raptor user updated: $out"; fi
grep -q "only root" <<<"$out" || fail "non-root: $out"

step "update to 1.0.0 from the stable channel"
out=$(raptor update) || fail "update: $out"
echo "$out"
grep -q "✓ Updated Wings 0.9.0 → 1.0.0" <<<"$out" || fail "update: $out"
[ "$(current)" = raptor-1.0.0 ] || fail "current is $(current)"
wait_up 1.0.0
# Until the next restart, the old version stays as the launcher.
[ "$(mainexe)" = "$lib/raptor-0.9.0" ] || fail "main process is $(mainexe)"
systemctl restart raptor-wings
wait_up 1.0.0
[ "$(mainexe)" = "$lib/raptor-1.0.0" ] || fail "after restart, main process is $(mainexe)"
raptor status | grep -q "Update   ✓ 0.9.0 → 1.0.0" || fail "status: $(raptor status)"
out=$(raptor update)
grep -q "1.0.0 is up to date" <<<"$out" || fail "not up to date: $out"

for v in 1.1.0:"exit status 3" 1.2.0:"not healthy within 45s"; do
	ver=${v%%:*}
	step "roll back from $ver (${v#*:})"
	if out=$(raptor update -version "$ver" 2>&1); then fail "update to $ver succeeded: $out"; fi
	echo "$out"
	grep -q "back on 1.0.0" <<<"$out" && grep -q "${v#*:}" <<<"$out" || fail "rollback: $out"
	[ "$(current)" = raptor-1.0.0 ] || fail "current is $(current)"
	[ ! -e "$lib/raptor-$ver" ] || fail "$ver left installed"
	wait_up 1.0.0
done

step "restart during a trial (1.0.1)"
raptor update -version 1.0.1 >"$work/cli.log" 2>&1 &
cli=$!
wait_up 1.0.1 # the trial is up, but not yet made current
[ "$(current)" = raptor-1.0.0 ] || fail "made current too early"
systemctl restart raptor-wings
wait "$cli" || fail "update: $(cat "$work/cli.log")"
cat "$work/cli.log"
grep -q "✓ Updated Wings 1.0.0 → 1.0.1" "$work/cli.log" || fail "update: $(cat "$work/cli.log")"
attempts=$(jq .attempts /var/lib/raptor/update.json)
[ "$attempts" = 2 ] || fail "attempts: $attempts"
[ "$(current)" = raptor-1.0.1 ] || fail "current is $(current)"
# Old versions are pruned: the one before stays for a manual rollback.
left=$(cd "$lib" && ls -d raptor-* | tr '\n' ' ')
[ "$left" = "raptor-1.0.0 raptor-1.0.1 " ] || fail "installed: $left"

step "the server kept running throughout"
[ "$(server)" = "$before" ] || fail "server changed: $before → $(server)"

echo
echo "PASS"
