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
# (No `| grep -q` after a long producer: with pipefail, grep exiting early
# fails the pipeline with SIGPIPE.)
wait_for() { for _ in $(seq 1 60); do if "$@"; then return 0; fi; sleep 1; done; return 1; }

mkdir -p /var/lib/raptor-e2e # survives the reboot, unlike /tmp

case "${1:-}" in
seed)
	# Docker's settings as the installer sets them (docs/WINGS.md#docker):
	# live-restore keeps containers running while Docker restarts, and no
	# userland proxy in front of published ports.
	if ! grep -q '"userland-proxy": false' /etc/docker/daemon.json 2>/dev/null ||
		! docker info -f '{{.LiveRestoreEnabled}}' | grep -q true; then
		echo '{"live-restore": true, "userland-proxy": false}' > /etc/docker/daemon.json
		systemctl restart docker
	fi
	systemctl stop raptor-wings
	# A unique name: servers from earlier runs stay (the CLI can't delete).
	NAME=host-test-$(date +%s)
	echo "$NAME" > /var/lib/raptor-e2e/host-name
	ID=$(RAPTOR_E2E_SEED_NAME=$NAME RAPTOR_E2E_SEED_DB=/var/lib/raptor/state.db /tmp/server-e2e -test.run '^TestSeedRealDaemon$' | awk '/^SEEDED/ {print $2}')
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
cli)
	# The CLI against the real daemon and socket, as root and as a member of
	# the raptor group (read-only).
	ID=$(cat /var/lib/raptor-e2e/host-id)
	NAME=$(cat /var/lib/raptor-e2e/host-name)
	SHORT=${ID: -8}
	getent passwd raptor-viewer >/dev/null || useradd --system --gid raptor --no-create-home --shell /usr/sbin/nologin raptor-viewer
	viewer() { runuser -u raptor-viewer -- "$@"; }
	state() { raptor ps -json | grep -o "\"id\":\"$ID\"[^}]*\"state\":\"[a-z_]*\"" | grep -o '"state":"[a-z_]*"' | cut -d'"' -f4; }
	is_state() { [ "$(state)" = "$1" ]; }
	ready() { echo "echo READY" | raptor console "$SHORT" >/dev/null; wait_for is_state running; }
	ready || fail "the server didn't become ready"

	out=$(raptor status)
	grep -Eq "Servers  [0-9]+, [0-9]+ up" <<<"$out" && grep -q "Storage  ✓" <<<"$out" || fail "raptor status: $out"
	pass "status: server counts and storage"

	out=$(raptor ps)
	echo "$out"
	grep -E "^$SHORT +$NAME +running +[0-9.]+% +[0-9.]+ [KMG]iB / 128.0 MiB" <<<"$out" >/dev/null || fail "raptor ps"
	pass "ps: short ID, state, CPU, memory"

	out=$(echo "echo cli-hello" | raptor console $NAME)
	grep -qx "cli-hello" <<<"$out" || fail "console: piped command's output missing: $out"
	grep -qx "READY" <<<"$out" || fail "console: no history"
	pass "console: history, then a piped command and its output"

	out=$(raptor logs "$ID" -n 2 -t)
	[ "$(wc -l <<<"$out")" = 2 ] && grep -q "cli-hello" <<<"$out" && grep -Eq "^[0-9]{4}-[0-9]{2}-[0-9]{2} " <<<"$out" || fail "logs -n 2 -t: $out"
	raptor logs $NAME -f >/tmp/raptor-follow.log &
	FOLLOW=$!
	echo "echo followed-line" | raptor console $NAME >/dev/null
	wait_for grep -q followed-line /tmp/raptor-follow.log || fail "logs -f didn't follow"
	kill $FOLLOW
	pass "logs: history with timestamps, and -f follows"

	out=$(viewer raptor ps) && grep -q $NAME <<<"$out" || fail "raptor group member can't list servers"
	viewer raptor logs $NAME -n 1 >/dev/null || fail "raptor group member can't read logs"
	out=$(viewer raptor stop $NAME 2>&1) && fail "raptor group member could stop a server"
	grep -q "only root" <<<"$out" || fail "unexpected refusal: $out"
	out=$(echo "echo nope" | viewer raptor console $NAME 2>&1) && fail "raptor group member could send a command"
	grep -q "only root" <<<"$out" || fail "unexpected refusal: $out"
	running || fail "server stopped by a refused request"
	pass "raptor group: read-only (ps, logs), power and commands refused"

	out=$(raptor stop $NAME)
	[ "$out" = "$NAME: offline" ] && ! running || fail "stop: $out"
	[ "$(docker inspect -f '{{.State.ExitCode}}' "raptor-$ID")" = 0 ] || fail "stop didn't use the egg's stop command"
	raptor start "$SHORT" >/dev/null && ready || fail "start"
	T=$(started)
	raptor restart $NAME >/dev/null && ready && [ "$(started)" != "$T" ] || fail "restart"
	raptor kill $NAME >/dev/null && ! running || fail "kill"
	raptor start $NAME >/dev/null && ready || fail "start after kill"
	pass "stop (graceful), start, restart, kill"

	out=$(raptor stop nope 2>&1) && fail "unknown server accepted"
	grep -q 'no server "nope"' <<<"$out" || fail "unknown server: $out"
	journalctl -u raptor-wings --no-pager -o cat | grep '"event":"server.power"' | grep '"user":"local:root"' >/dev/null || fail "power actions not attributed to local:root"
	pass "unknown servers refused; power actions attributed to the Unix user"

	# The server's user can read its machine-id (Wings runs with UMask=0077).
	[ "$(docker exec "raptor-$ID" cat /etc/machine-id)" = "${ID//-/}" ] || fail "/etc/machine-id isn't the server ID without dashes, or isn't readable"
	pass "/etc/machine-id: the server ID, readable by the server"

	# Backups through the daemon's real worker: a transient scope started
	# from inside the sandboxed service.
	file=/var/lib/raptor/volumes/$ID/cli-backup.txt
	docker exec "raptor-$ID" sh -c 'echo before > /home/container/cli-backup.txt'
	out=$(raptor backup create $NAME 2>&1) || fail "backup create: $out"
	BID=$(grep -o '^backup [0-9a-f]*' <<<"$out" | cut -d' ' -f2)
	[ -n "$BID" ] || fail "backup create: $out"
	out=$(viewer raptor backup list $NAME) && grep -Eq "^$BID +[0-9: -]+ +manual +ok" <<<"$out" || fail "backup list: $out"
	out=$(viewer raptor backup create $NAME 2>&1) && fail "raptor group member could make a backup"
	journalctl --no-pager -o cat | grep "Started run-.*scope - /usr/local/bin/raptor wings backup-worker" >/dev/null || fail "the backup worker didn't run in its own scope"
	docker exec "raptor-$ID" sh -c 'echo after > /home/container/cli-backup.txt'
	out=$(raptor backup restore $NAME "$BID" </dev/null 2>&1) && fail "restored without confirmation"
	out=$(raptor backup restore $NAME "$BID" -yes 2>&1) || fail "backup restore: $out"
	grep -q "safety backup" <<<"$out" || fail "no safety backup: $out"
	[ "$(cat "$file")" = before ] || fail "restore didn't bring the file back: $(cat "$file")"
	ready || fail "server didn't start again after the restore"
	pass "backup create, list (read-only for the raptor group), restore with a safety backup, in the worker's scope"
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
	echo "usage: $0 seed|cli|after-reboot" >&2
	exit 2
	;;
esac
