#!/bin/bash
# Disk quota validation benchmark (docs/ROADMAP.md 1.6): the host's native
# filesystem vs an XFS volume in a loop-mounted image file (quota tier 2),
# with the I/O patterns game servers use. Run as root on a real Linux host;
# VMs on macOS don't give fsync real-disk semantics.
set -euo pipefail
DIR=${1:-/var/lib/raptor-bench}
RUNS=${RUNS:-5}
mkdir -p "$DIR/native"
command -v fio >/dev/null || { apt-get update -qq && apt-get install -y -qq fio >/dev/null; }

img="$DIR/volume.xfs"; mnt="$DIR/xfs"
fallocate -l 4G "$img"
mkfs.xfs -q -f "$img"
mkdir -p "$mnt"
mount -o loop,prjquota,noatime "$img" "$mnt"
dev=$(findmnt -no SOURCE "$mnt")
losetup --direct-io=on "$dev"
trap 'umount "$mnt"; rm -f "$img"' EXIT
echo "native: $(findmnt -no FSTYPE,SOURCE --target "$DIR/native")   volume: xfs on $dev (direct I/O $(cat /sys/block/$(basename "$dev")/loop/dio))"

measure() { # dir fio-args... -> median IOPS
  local d=$1; shift
  for _ in $(seq "$RUNS"); do
    fio --name=b --directory="$d" --size=1G --runtime=20 --time_based --output-format=json "$@" 2>/dev/null |
      python3 -c 'import json,sys; j=json.load(sys.stdin)["jobs"][0]; print(round(j["read"]["iops"]+j["write"]["iops"]))'
    rm -f "$d"/b.*
  done | sort -n | awk '{a[NR]=$1} END {print a[int((NR+1)/2)]}'
}

printf "%-18s %12s %12s %7s\n" test native xfs-loop ratio
while IFS='|' read -r name args; do
  # shellcheck disable=SC2086
  n=$(measure "$DIR/native" $args); x=$(measure "$mnt" $args)
  printf "%-18s %12s %12s %6s%%\n" "$name" "$n" "$x" "$(( 100 * x / n ))"
done <<'TESTS'
seq-write-1M|--rw=write --bs=1M --direct=1 --iodepth=8 --ioengine=libaio
seq-read-1M|--rw=read --bs=1M --direct=1 --iodepth=8 --ioengine=libaio
rand-write-4k|--rw=randwrite --bs=4k --direct=1 --iodepth=16 --ioengine=libaio
rand-read-4k|--rw=randread --bs=4k --direct=1 --iodepth=16 --ioengine=libaio
save-16k-fsync|--rw=randwrite --bs=16k --fsync=1 --ioengine=psync
buffered-write|--rw=write --bs=64k --ioengine=psync --end_fsync=1
TESTS
