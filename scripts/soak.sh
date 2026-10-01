#!/bin/bash
# Soak test (Phase 2's exit criterion), run as root in its own Wings VM:
# servers with scheduled restarts and backups, Wings restarted at random,
# and a monitor recording every server's container each minute. `report`
# says whether any game went down when it shouldn't have.
#
#   soak.sh start     seed servers, start the chaos and the monitor
#   soak.sh report    what happened so far (exit 1 on unexpected downtime)
#   soak.sh stop      stop the chaos and the monitor
#
# Settings (start): SOAK_SERVERS (3), SOAK_RESTART and SOAK_BACKUP (cron;
# every 6 h and every 2 h), SOAK_CHAOS_MIN and SOAK_CHAOS_MAX (seconds
# between Wings restarts; 1 h and 6 h).
set -euo pipefail

D=/var/lib/raptor-soak
mkdir -p "$D"

case "${1:-}" in
start)
	systemctl stop raptor-wings
	RAPTOR_SOAK_DB=/var/lib/raptor/state.db RAPTOR_SOAK_SERVERS=${SOAK_SERVERS:-3} \
		RAPTOR_SOAK_RESTART=${SOAK_RESTART:-0 */6 * * *} RAPTOR_SOAK_BACKUP=${SOAK_BACKUP:-0 */2 * * *} \
		/tmp/actions-e2e -test.run '^TestSeedSoak$' | awk '/^SOAK/ {print $2}' >"$D/servers"
	[ -s "$D/servers" ] || { echo "seeding failed"; exit 1; }
	systemctl start raptor-wings
	date +%s >"$D/started"
	: >"$D/monitor.log"
	: >"$D/chaos.log"
	# Every minute: each server's container state and start time.
	systemd-run --unit raptor-soak-monitor --collect bash -c "
		while true; do
			for id in \$(cat $D/servers); do
				echo \"\$(date +%s) \$id \$(docker inspect -f '{{.State.Running}} {{.State.StartedAt}}' raptor-\$id 2>/dev/null || echo missing -)\" >>$D/monitor.log
			done
			sleep 60
		done" >/dev/null
	# Wings restarted at random.
	min=${SOAK_CHAOS_MIN:-3600}
	max=${SOAK_CHAOS_MAX:-21600}
	systemd-run --unit raptor-soak-chaos --collect bash -c "
		while true; do
			sleep \$(( $min + (RANDOM * 32768 + RANDOM) % ($max - $min + 1) ))
			systemctl restart raptor-wings && date +%s >>$D/chaos.log
		done" >/dev/null
	echo "soak started: $(wc -l <"$D/servers") servers; see soak.sh report"
	;;
stop)
	systemctl stop raptor-soak-chaos raptor-soak-monitor 2>/dev/null || true
	echo "soak stopped"
	;;
report)
	[ -s "$D/servers" ] || { echo "no soak running (soak.sh start)"; exit 1; }
	start=$(cat "$D/started")
	hours=$(( ($(date +%s) - start) / 3600 ))
	echo "Soak: ${hours} h so far, $(wc -l <"$D/chaos.log") random Wings restarts, Wings $(systemctl is-active raptor-wings)"
	journal=$(journalctl -u raptor-wings --no-pager -o cat --since "@$start")
	failed=0
	for id in $(cat "$D/servers"); do
		samples=$(grep -c " $id " "$D/monitor.log" || true)
		down=$(grep " $id " "$D/monitor.log" | grep -vc " $id true " || true)
		# Container (re)starts the monitor saw, after the first.
		starts=$(grep " $id true " "$D/monitor.log" | awk '{print $4}' | uniq | wc -l)
		observed=$((starts > 0 ? starts - 1 : 0))
		# Restarts the server's schedule asked for (power actions by schedule:).
		scheduled=$(grep '"event":"server.power"' <<<"$journal" | grep "\"server\":\"$id\"" | grep -c '"user":"schedule:' || true)
		backups=$(raptor backup list "$id" 2>/dev/null | awk 'NR>1')
		ok=$(grep -c ' ok ' <<<"$backups" || true)
		bad=$(grep -c ' failed ' <<<"$backups" || true)
		unexpected=$((observed - scheduled))
		verdict=ok
		# A scheduled restart can fall on a sample; anything beyond that, or
		# a restart nobody scheduled, is unexpected downtime.
		if [ "$unexpected" -gt 0 ] || [ "$down" -gt "$scheduled" ] || [ "$bad" -gt 0 ]; then
			verdict=FAIL
			failed=1
		fi
		printf "%s  %s: %d samples, %d down, %d restarts (%d scheduled), backups %d ok / %d failed\n" \
			"$verdict" "${id: -8}" "$samples" "$down" "$observed" "$scheduled" "$ok" "$bad"
	done
	exit $failed
	;;
*)
	echo "usage: soak.sh start|report|stop" >&2
	exit 2
	;;
esac
