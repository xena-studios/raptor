#!/bin/bash
# Host-level lifecycle test, run as root in the Wings VM (task e2e:host).
# Checks the Phase 1 promises against the real systemd units and Docker:
# Wings and Docker restarts never restart servers, a host shutdown stops them
# gracefully without marking them stopped, and they come back afterwards.
set -euo pipefail

started() { docker inspect -f '{{.State.StartedAt}}' "raptor-$ID"; }
running() { [ "$(docker inspect -f '{{.State.Running}}' "raptor-$ID")" = true ]; }
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; journalctl -u raptor-wings --no-pager -n 30 -o cat; exit 1; }
wait_for() { for _ in $(seq 1 60); do if "$@"; then return 0; fi; sleep 1; done; return 1; }

mkdir -p /var/lib/raptor-e2e # survives the reboot, unlike /tmp

case "${1:-}" in
seed)
	# Docker's live-restore keeps containers running while Docker restarts
	# (the installer sets it; see docs/WINGS.md).
	if ! docker info -f '{{.LiveRestoreEnabled}}' | grep -q true; then
		echo '{"live-restore": true}' > /etc/docker/daemon.json
		systemctl restart docker
	fi
	systemctl stop raptor-wings
	ID=$(RAPTOR_E2E_SEED_DB=/var/lib/raptor/state.db /tmp/server-e2e -test.run '^TestSeedRealDaemon$' | awk '/^SEEDED/ {print $2}')
	[ -n "$ID" ] || fail "seeding a server"
	echo "$ID" > /var/lib/raptor-e2e/host-id
	T0=$(started)
	systemctl start raptor-wings
	sleep 3
	[ "$(started)" = "$T0" ] && running || fail "Wings start restarted or lost the server"
	pass "Wings adopted the running server without restarting it"

	systemctl restart raptor-wings
	sleep 3
	[ "$(started)" = "$T0" ] && running || fail "systemctl restart raptor-wings restarted the server"
	pass "systemctl restart raptor-wings: server kept running"

	systemctl restart docker
	sleep 5
	[ "$(started)" = "$T0" ] && running || fail "systemctl restart docker restarted the server"
	pass "systemctl restart docker: server kept running (live-restore)"

	systemctl stop raptor-shutdown
	running && fail "host shutdown didn't stop the server"
	[ "$(docker inspect -f '{{.State.ExitCode}}' "raptor-$ID")" = 0 ] || fail "server wasn't stopped with its stop command"
	pass "host shutdown: server stopped gracefully (exit 0 from the egg's stop command)"
	systemctl start raptor-shutdown

	systemctl restart raptor-wings
	wait_for running || fail "server didn't come back after the host shutdown"
	[ "$(started)" != "$T0" ] || fail "server wasn't started again"
	pass "server started again afterwards (desired_state stayed running)"
	started > /var/lib/raptor-e2e/host-t1
	;;
after-reboot)
	ID=$(cat /var/lib/raptor-e2e/host-id)
	wait_for running || fail "server didn't come back after the reboot"
	[ "$(started)" != "$(cat /var/lib/raptor-e2e/host-t1)" ] || fail "container wasn't restarted by the reboot?"
	pass "reboot: Wings started the server again"
	# Look only at the reboot's shutdown (after "System is powering down"),
	# not the manual host-shutdown test earlier in the same boot. There,
	# raptor-shutdown must have stopped the servers, and systemd must not
	# have stopped any container scope before it finished.
	log=$(journalctl -b -1 --no-pager -o cat | sed -n '/System is powering down/,$p')
	[ -n "$log" ] || fail "no shutdown found in the previous boot's journal"
	stopped=$(grep -m1 -o "stopped [0-9]* server" <<<"$log" || true)
	[ -n "$stopped" ] && [ "$stopped" != "stopped 0 server" ] || fail "raptor-shutdown didn't stop servers during the reboot ($stopped)"
	done_at=$(grep -n -m1 "Stopped raptor-shutdown.service" <<<"$log" | cut -d: -f1 || true)
	[ -n "$done_at" ] || fail "raptor-shutdown didn't finish during the reboot"
	early=$(head -n "$done_at" <<<"$log" | grep -c "Stopping docker-.*\.scope" || true)
	[ "$early" = 0 ] || fail "systemd stopped $early container scope(s) before raptor-shutdown finished"
	echo "  reboot: $stopped(s) gracefully; container scopes stopped before that: $early"
	pass "reboot: raptor-shutdown stopped servers gracefully before systemd or Docker touched them"
	;;
*)
	echo "usage: $0 seed|after-reboot" >&2
	exit 2
	;;
esac
