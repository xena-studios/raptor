#!/bin/bash
# A pgBackRest backup of the primary (docs/DEPLOY.md#backups): full on
# Sundays, differential otherwise. WAL is archived continuously as well.
#   backup.sh [full|diff]
set -euo pipefail
# The stack holding the primary: server #2's after a failover (ROLE=replica
# in /etc/raptor/stack.env).
cd "$(dirname "$0")/../${ROLE:-primary}"
type=${1:-$([ "$(date +%u)" = 7 ] && echo full || echo diff)}
docker compose exec -T -u postgres postgres pgbackrest --stanza=raptor --type="$type" backup
docker compose exec -T -u postgres postgres pgbackrest --stanza=raptor info
# A dead man's switch (docs/DEPLOY.md#monitoring): it alerts when the pings
# stop, which catches a timer that never ran as well as a run that failed.
if [ -n "${BACKUP_PING_URL:-}" ]; then curl -fsS -m 10 --retry 3 "$BACKUP_PING_URL" >/dev/null || true; fi
