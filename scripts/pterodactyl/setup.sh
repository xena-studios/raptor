#!/bin/bash
# Stands up the real Pterodactyl stack for the behavioral diff
# (internal/wings/conformance, TestPterodactylDiff): the Panel with its
# database and cache in Docker, and Pterodactyl Wings on the host, linked as
# a node. Test-only: fixed passwords, no TLS. Run as root on a box that runs
# Docker (the Wings VM, or a CI runner). Writes what the test needs to
# $OUT (default /var/lib/raptor-e2e/pterodactyl.json).
#
#   setup.sh [egg files to import...]    set up (idempotent) and import eggs
#   setup.sh down                        remove everything it created
set -euo pipefail

PANEL_VERSION=${PTERODACTYL_PANEL_VERSION:-v1.15.1}
WINGS_VERSION=${PTERODACTYL_WINGS_VERSION:-v1.13.3}
NET=raptor-ptero
SUBNET=172.31.200.0/24
GATEWAY=172.31.200.1
PANEL_URL=http://127.0.0.1:8090
OUT=${OUT:-/var/lib/raptor-e2e/pterodactyl.json}
WINGS_PORT=8080

log() { echo "[pterodactyl] $*" >&2; }

if [ "${1:-}" = down ]; then
	systemctl stop raptor-ptero-wings 2>/dev/null || true
	# Servers Pterodactyl Wings created, and their files.
	docker ps -aq --filter label=Service=Pterodactyl | xargs -r docker rm -f >/dev/null
	rm -rf /var/lib/pterodactyl /tmp/pterodactyl
	docker rm -f raptor-ptero-panel raptor-ptero-db raptor-ptero-cache 2>/dev/null || true
	docker volume rm raptor-ptero-var 2>/dev/null || true
	docker network rm $NET 2>/dev/null || true
	exit 0
fi

# --- Panel, database, cache -------------------------------------------------
docker network inspect $NET >/dev/null 2>&1 ||
	docker network create --subnet $SUBNET --gateway $GATEWAY $NET >/dev/null
if ! docker inspect raptor-ptero-db >/dev/null 2>&1; then
	docker run -d --name raptor-ptero-db --network $NET --network-alias database \
		-e MYSQL_DATABASE=panel -e MYSQL_USER=pterodactyl -e MYSQL_PASSWORD=diff-only \
		-e MYSQL_ROOT_PASSWORD=diff-only-root mariadb:11 >/dev/null
	docker run -d --name raptor-ptero-cache --network $NET --network-alias cache redis:alpine >/dev/null
	log "waiting for the database"
	until docker exec raptor-ptero-db healthcheck.sh --connect --innodb_initialized >/dev/null 2>&1; do sleep 2; done
	# /app/var holds the generated app key; the image needs it mounted.
	docker run -d --name raptor-ptero-panel --network $NET -p 127.0.0.1:8090:80 -v raptor-ptero-var:/app/var \
		-e APP_URL=$PANEL_URL -e APP_TIMEZONE=UTC -e APP_SERVICE_AUTHOR=diff@raptor.invalid \
		-e APP_ENV=production -e APP_ENVIRONMENT_ONLY=false \
		-e CACHE_DRIVER=redis -e SESSION_DRIVER=redis -e QUEUE_DRIVER=redis -e REDIS_HOST=cache \
		-e DB_HOST=database -e DB_PORT=3306 -e DB_PASSWORD=diff-only -e HASHIDS_LENGTH=8 \
		-e MAIL_DRIVER=array \
		ghcr.io/pterodactyl/panel:$PANEL_VERSION >/dev/null
fi
log "waiting for the Panel"
for _ in $(seq 1 150); do
	[ "$(curl -s -o /dev/null -w '%{http_code}' $PANEL_URL/auth/login)" = 200 ] && break
	sleep 2
done
artisan() { docker exec raptor-ptero-panel php artisan "$@"; }
tinker() { docker exec raptor-ptero-panel php artisan tinker --execute="$1"; }

# --- User, keys, location, node, allocations --------------------------------
if ! tinker 'echo \Pterodactyl\Models\User::count();' | grep -q '^[1-9]'; then
	artisan p:user:make --email=diff@raptor.invalid --username=diff --name-first=Diff \
		--name-last=Test --password=diff-only-Passw0rd --admin=1 --no-interaction >/dev/null
	artisan p:location:make --short=diff --long=diff --no-interaction >/dev/null
	artisan p:node:make --name=diff --description=diff --locationId=1 --fqdn=$GATEWAY --public=1 \
		--scheme=http --proxy=0 --maintenance=0 --maxMemory=16384 --overallocateMemory=-1 \
		--maxDisk=100000 --overallocateDisk=-1 --uploadSize=100 \
		--daemonListeningPort=$WINGS_PORT --daemonSFTPPort=2023 --daemonBase=/var/lib/pterodactyl/volumes \
		--no-interaction >/dev/null
fi
# API keys, made with the Panel's own service (there's no command for them).
keys=$(tinker '
$svc = app(\Pterodactyl\Services\Api\KeyCreationService::class);
$app = $svc->setKeyType(\Pterodactyl\Models\ApiKey::TYPE_APPLICATION)->handle(["memo" => "diff", "user_id" => 1, "allowed_ips" => []],
  ["r_servers" => 3, "r_nodes" => 3, "r_allocations" => 3, "r_users" => 3, "r_locations" => 3, "r_nests" => 3, "r_eggs" => 3, "r_database_hosts" => 3, "r_server_databases" => 3]);
$cli = app(\Pterodactyl\Services\Api\KeyCreationService::class)->setKeyType(\Pterodactyl\Models\ApiKey::TYPE_ACCOUNT)->handle(["memo" => "diff", "user_id" => 1, "allowed_ips" => []]);
echo "APP=".$app->identifier.decrypt($app->token)."\n"."CLIENT=".$cli->identifier.decrypt($cli->token)."\n";')
APP_KEY=$(sed -n 's/^APP=//p' <<<"$keys")
CLIENT_KEY=$(sed -n 's/^CLIENT=//p' <<<"$keys")
[ -n "$APP_KEY" ] && [ -n "$CLIENT_KEY" ] || { echo "$keys" >&2; exit 1; }
api() { curl -sf -H "Authorization: Bearer $APP_KEY" -H 'Accept: application/json' -H 'Content-Type: application/json' "$@"; }
if [ "$(api $PANEL_URL/api/application/nodes/1/allocations | jq '.meta.pagination.total')" = 0 ]; then
	api -X POST $PANEL_URL/api/application/nodes/1/allocations \
		-d "{\"ip\":\"0.0.0.0\",\"ports\":[\"30000-30099\"]}" >/dev/null
fi

# --- Pterodactyl Wings --------------------------------------------------------
arch=$(dpkg --print-architecture)
if [ ! -x /usr/local/bin/pterodactyl-wings ]; then
	curl -sfL -o /usr/local/bin/pterodactyl-wings \
		https://github.com/pterodactyl/wings/releases/download/$WINGS_VERSION/wings_linux_$arch
	chmod +x /usr/local/bin/pterodactyl-wings
fi
mkdir -p /etc/pterodactyl
artisan p:node:configuration 1 >/etc/pterodactyl/config.yml
# Reach the Panel from the host, and stay clear of Raptor's ports.
sed -i "s|^remote: .*|remote: $PANEL_URL|" /etc/pterodactyl/config.yml
sed -i "s|bind_port: 2022|bind_port: 2023|" /etc/pterodactyl/config.yml
if ! systemctl is-active -q raptor-ptero-wings; then
	systemctl reset-failed raptor-ptero-wings 2>/dev/null || true
	systemd-run --unit raptor-ptero-wings --collect /usr/local/bin/pterodactyl-wings --config /etc/pterodactyl/config.yml >/dev/null
fi
log "waiting for Pterodactyl Wings"
for _ in $(seq 1 60); do
	api $PANEL_URL/api/application/nodes/1?include=servers >/dev/null &&
		curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:$WINGS_PORT/api/system | grep -q 401 && break
	sleep 2
done

# --- Eggs ---------------------------------------------------------------------
eggs='{}'
for f in "$@"; do
	name=$(basename "$f")
	docker cp "$f" raptor-ptero-panel:/tmp/"$name"
	id=$(tinker "
\$f = new \Illuminate\Http\UploadedFile('/tmp/$name', '$name', 'application/json', null, true);
echo app(\Pterodactyl\Services\Eggs\Sharing\EggImporterService::class)->handle(\$f, 1)->id;" | tail -1)
	eggs=$(jq --arg n "$name" --argjson id "$id" '. + {($n): $id}' <<<"$eggs")
	log "imported $name as egg $id"
done

mkdir -p "$(dirname "$OUT")"
jq -n --arg url "$PANEL_URL" --arg app "$APP_KEY" --arg client "$CLIENT_KEY" --argjson eggs "$eggs" \
	'{panel_url: $url, application_key: $app, client_key: $client, node: 1, nest: 1, eggs: $eggs}' >"$OUT"
log "ready: $OUT"
