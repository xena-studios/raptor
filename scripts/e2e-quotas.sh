#!/bin/bash
# Disk quota tests, run as root on a Linux box with Docker (task e2e:quotas,
# CI): a real XFS loop volume made by Wings' own code, limits on the host and
# in containers, the seccomp escape check, the server manager on top, the
# boot-time mount, and online growth.
#
#   e2e-quotas.sh <storage-e2e> <server-e2e> [steps...]
#   steps: fresh (default: fresh test grow) | test | after-reboot | grow
set -euo pipefail

STORAGE=$1 SERVER=$2
shift 2
VOL=/var/lib/raptor-gate/volumes
UNIT=$(systemd-escape --path "$VOL").mount

command -v mkfs.xfs >/dev/null || { apt-get update -qq && apt-get install -y -qq xfsprogs >/dev/null; }

for step in "${@:-fresh test grow}"; do
	for s in $step; do
		case "$s" in
		fresh)
			systemctl disable --now "$UNIT" 2>/dev/null || true
			rm -f "/etc/systemd/system/$UNIT" /var/lib/raptor-gate/volumes.xfs
			systemctl daemon-reload
			RAPTOR_E2E_GATE_STEP=setup "$STORAGE" -test.v -test.run '^TestGateSetupVolume$'
			;;
		test)
			RAPTOR_E2E_VOLUME=$VOL "$STORAGE" -test.v -test.timeout 10m -test.run '^TestGate(HostLimit|ContainerLimit|Escape)'
			RAPTOR_E2E_VOLUME=$VOL "$SERVER" -test.v -test.timeout 10m -test.run '^(TestDiskQuota|TestVolumeUnavailable)$'
			;;
		after-reboot | grow)
			RAPTOR_E2E_GATE_STEP=$s "$STORAGE" -test.v -test.run '^TestGate(AfterReboot|GrowOnline)$'
			;;
		*)
			echo "unknown step $s" >&2
			exit 2
			;;
		esac
	done
done
