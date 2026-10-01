#!/bin/bash
# Fault injection, run as root in a THROWAWAY Wings VM (task e2e:faults):
# it kills Wings and Docker, fills the host disk and the data volume,
# unmounts the volume, and corrupts state.db, and checks after each that
# Wings did what docs/RELIABILITY.md promises, not just that nothing
# crashed. The node connection's faults come with the connection (Phase 3);
# reboots are in e2e:host.
set -euo pipefail

VOL=/var/lib/raptor/volumes
STATE=/var/lib/raptor/state.db
FILL=/var/tmp/raptor-faults-fill

pass() { echo "PASS: $*"; }
fail() {
	echo "FAIL: $*"
	rm -f "$FILL" "$FILL"2
	journalctl -u raptor-wings --no-pager -n 40 -o cat
	exit 1
}
wait_for() { # seconds, command…
	local n=$1
	shift
	for _ in $(seq 1 "$n"); do if "$@"; then return 0; fi; sleep 1; done
	return 1
}
started() { docker inspect -f '{{.State.StartedAt}}' "raptor-$ID"; }
running() { [ "$(docker inspect -f '{{.State.Running}}' "raptor-$ID" 2>/dev/null)" = true ]; }
active() { systemctl is-active -q raptor-wings; }
answers() { raptor status >/dev/null 2>&1; }
state() { raptor ps -json | grep -o "\"id\":\"$ID\"[^}]*\"state\":\"[a-z_]*\"" | grep -o '"state":"[a-z_]*"' | cut -d'"' -f4; }
is_state() { [ "$(state)" = "$1" ]; }
ready() { echo "echo READY" | raptor console "$ID" >/dev/null; wait_for 60 is_state running; }
backup_ok() { raptor backup list "$ID" | grep -E "^$1 " | grep -q ' ok '; }
cleanup_fill() { rm -f "$FILL" "$FILL"2; }
trap cleanup_fill EXIT

# --- A running server ---------------------------------------------------------
# Docker's settings as the installer sets them (docs/WINGS.md#docker).
if ! grep -q '"userland-proxy": false' /etc/docker/daemon.json 2>/dev/null ||
	! docker info -f '{{.LiveRestoreEnabled}}' | grep -q true; then
	echo '{"live-restore": true, "userland-proxy": false}' >/etc/docker/daemon.json
	systemctl restart docker
fi
systemctl stop raptor-wings
ID=$(RAPTOR_E2E_SEED_NAME=faults-$(date +%s) RAPTOR_E2E_SEED_DISK_MIB=256 RAPTOR_E2E_SEED_DB=$STATE \
	/tmp/server-e2e -test.run '^TestSeedRealDaemon$' | awk '/^SEEDED/ {print $2}')
[ -n "$ID" ] || fail "seeding a server"
systemctl start raptor-wings
wait_for 60 answers || fail "Wings didn't start"
ready || fail "the server didn't become ready"
DIR=$VOL/$ID
OWNER=$(stat -c %u:%g "$DIR")
pass "seeded server $ID"

# --- Wings killed in the middle of a backup ------------------------------------
dd if=/dev/urandom of="$DIR/big.bin" bs=1M count=150 status=none
chown "$OWNER" "$DIR/big.bin"
T0=$(started)
raptor backup create "$ID" >/tmp/faults-backup.log 2>&1 &
wait_for 30 grep -q . /tmp/faults-backup.log || wait_for 10 bash -c "raptor backup list $ID | grep -q -E 'running|pending'" || true
sleep 2
systemctl kill -s KILL raptor-wings
wait_for 60 active || fail "systemd didn't restart Wings after SIGKILL"
wait_for 60 answers || fail "Wings didn't answer after SIGKILL"
BACKUP=$(raptor backup list "$ID" | awk 'NR>1 {print $1; exit}')
[ -n "$BACKUP" ] || fail "no backup recorded"
wait_for 240 backup_ok "$BACKUP" || fail "the interrupted backup didn't finish after Wings came back: $(raptor backup list "$ID")"
[ "$(started)" = "$T0" ] && running || fail "the server was restarted by Wings' crash"
pass "Wings SIGKILLed mid-backup: the backup resumed and finished, the server never restarted"

# --- Docker killed -------------------------------------------------------------
systemctl kill -s KILL docker.service
wait_for 90 systemctl is-active -q docker || fail "Docker didn't come back"
wait_for 30 running || fail "the server didn't survive Docker's crash"
[ "$(started)" = "$T0" ] || fail "the server was restarted with Docker (live-restore off?)"
wait_for 60 bash -c "echo 'echo after-docker' | raptor console $ID | grep -qx after-docker" || fail "the console didn't work after Docker came back"
pass "Docker SIGKILLed: the server kept running, Wings reattached its console"

# --- The host disk fills up ----------------------------------------------------
avail() { df --output=avail -B1 /var/lib/raptor | tail -1; }
fallocate -l $(($(avail) - 512 * 1024 * 1024)) "$FILL"
wait_for 90 bash -c "raptor status | grep -q 'Disk     ✗'" || fail "raptor status didn't show the low disk: $(raptor status)"
out=$(raptor backup create "$ID" 2>&1) && fail "a local backup was taken with the disk low: $out"
grep -qi "free disk space" <<<"$out" || fail "low disk backup refusal: $out"
running || fail "the server stopped when the disk ran low"
# Completely full: Wings must keep running and answering.
fallocate -l "$(avail)" "$FILL"2 2>/dev/null || dd if=/dev/zero of="$FILL"2 bs=1M status=none 2>/dev/null || true
sleep 20
active && answers || fail "Wings didn't survive a full disk"
running || fail "the server stopped on a full disk"
cleanup_fill
wait_for 90 bash -c "raptor status | grep -q 'Disk     ✓'" || fail "raptor status didn't show the disk recovered"
raptor backup create "$ID" >/dev/null || fail "a backup failed after the disk was freed"
journalctl -u raptor-wings --no-pager -o cat | grep '"type":"node.disk_low"\|host disk low' >/dev/null || fail "no host disk alert in the log"
pass "host disk full: backups refused, Wings and the server kept running, recovered after freeing it"

# --- The server fills its disk quota ---------------------------------------------
# A start through the real Wings applies the quota (the seeding above ran
# without quotas, and adopting a running server doesn't change them).
raptor restart "$ID" >/dev/null && ready || fail "restart before the quota test"
out=$(docker exec "raptor-$ID" sh -c 'dd if=/dev/zero of=/home/container/fill bs=1M count=1024 2>&1' || true)
grep -Eqi "quota|no space" <<<"$out" || fail "writing past the server's 256 MiB limit: $out"
active && answers || fail "Wings didn't survive a full quota"
docker exec "raptor-$ID" rm -f /home/container/fill
pass "server quota full: its writes failed with $(grep -Eoi 'Disk quota exceeded|No space left on device' <<<"$out" | head -1), Wings unaffected"

# --- The data volume disappears ---------------------------------------------------
raptor stop "$ID" >/dev/null
umount "$VOL" || fail "unmounting $VOL"
wait_for 30 bash -c "raptor status | grep -q 'Storage  ✗'" || fail "raptor status didn't show the missing volume: $(raptor status)"
out=$(raptor start "$ID" 2>&1) && fail "a server started without its volume: $out"
grep -qi "mount\|volume" <<<"$out" || fail "start without the volume: $out"
[ ! -e "$VOL/$ID" ] || fail "something was written into the bare mount point"
systemctl start "$(systemd-escape -p --suffix=mount "$VOL")" || mount "$VOL" || fail "remounting $VOL"
wait_for 30 bash -c "raptor status | grep -q 'Storage  ✓'" || fail "raptor status didn't show the volume back"
raptor start "$ID" >/dev/null && ready || fail "the server didn't start after the volume came back"
pass "volume unmounted: starts refused with a clear error, nothing written underneath, recovered on remount"

# --- state.db corrupted --------------------------------------------------------------
T1=$(started)
systemctl stop raptor-wings # writes a shutdown snapshot
dd if=/dev/urandom of="$STATE" bs=4096 count=8 seek=1 conv=notrunc status=none
rm -f "$STATE"-wal "$STATE"-shm
systemctl start raptor-wings
wait_for 60 answers || fail "Wings didn't start after state.db was corrupted"
journalctl -u raptor-wings --no-pager -o cat --since "-2min" | grep "replaced by its newest good snapshot" >/dev/null || fail "no recovery in the log"
ls "$STATE".corrupt-* >/dev/null 2>&1 || fail "the corrupt database wasn't kept"
is_state running || [ "$(started)" = "$T1" ] || fail "the server was lost or restarted after the recovery: $(raptor ps)"
raptor ps | grep -q "${ID: -8}" || fail "the server is missing after the recovery"
pass "state.db corrupted: restored from the shutdown snapshot, the server kept running and is still known"

echo "ALL FAULTS PASSED"
