#!/bin/bash
# Pings a dead man's switch while the disk Postgres lives on has room
# (docs/DEPLOY.md#monitoring), hourly from raptor-disk-check.timer. On one
# server a full disk stops the database, and nothing else would notice: the
# Panel's container can't see the host's disks. Over the limit it reports a
# failure instead, so the check alerts at once rather than after its grace.
set -euo pipefail
LIMIT=${DISK_LIMIT_PERCENT:-85}
dir=$(docker volume inspect -f '{{.Mountpoint}}' raptor_pgdata 2>/dev/null || echo /var/lib/docker)
used=$(df --output=pcent "$dir" | tail -1 | tr -dc 0-9)
echo "disk-check: $used% used where Postgres keeps its data (limit $LIMIT%)"
[ -n "${DISK_PING_URL:-}" ] || exit 0
if [ "$used" -lt "$LIMIT" ]; then
  curl -fsS -m 10 --retry 3 "$DISK_PING_URL" >/dev/null || true
else
  curl -fsS -m 10 --retry 3 --data-raw "$used% used" "$DISK_PING_URL/fail" >/dev/null || true
  exit 1
fi
