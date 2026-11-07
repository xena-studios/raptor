#!/bin/bash
# Rehearses the production setup on one machine (task deploy:rehearse): the
# same compose files, images, and scripts as the servers, with local stand-ins
# for what isn't here: MinIO for object storage, a local CA for Cloudflare's
# origin certificates, plain files for 1Password, and a Docker network for
# MinIO's network. It checks, and fails loudly:
#   1. the Panel answers through Caddy only with Cloudflare's client cert;
#   2. pgBackRest archives WAL and backs up to object storage;
#   3. a restore of that backup matches the live database (restore-test.sh);
#   4. a rolling deploy (deploy.sh) never stops answering;
#   5. with the server gone, volumes and all, restore.sh rebuilds it from
#      the archive with nothing lost, and it backs up again.
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT=$(cd .. && pwd)
W=$(mktemp -d /tmp/raptor-rehearsal.XXXXXX)
NET=raptor-rehearsal
P="docker compose -p raptor-rehearsal -f panel/compose.yaml -f rehearsal/panel.yaml"
# What the scripts on the server see, pointed at the rehearsal's stack.
export COMPOSE_PROJECT_NAME=raptor-rehearsal COMPOSE_FILE=compose.yaml:../rehearsal/panel.yaml
export ENV_FILE="$W/run/postgres.env" NETWORK=raptor-rehearsal SKIP_PULL=1 SKIP_SECRETS=1 SKIP_UNITS=1
export RAPTOR_RUN=$W/run RAPTOR_TLS=$W/tls HTTPS_PORT=${HTTPS_PORT:-18443} API_HOST=api.localhost
export PANEL_IMAGE=raptor-panel:rehearsal POSTGRES_IMAGE=raptor-postgres:rehearsal
export PANEL_APP_URL=https://app.example.test PANEL_API_URL=https://api.localhost
step() { printf '\n== %s\n' "$*"; }
fail() { echo "REHEARSAL FAILED: $*" >&2; exit 1; }

cleanup() {
  if [ "${KEEP:-}" = 1 ]; then echo "kept: $W (KEEP=1)"; return; fi
  $P down -v >/dev/null 2>&1 || true
  docker rm -f raptor-rehearsal-minio >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$W"
  rm -f panel/.env
}
trap cleanup EXIT

step "images"
docker build -q -f images/panel.Dockerfile -t "$PANEL_IMAGE" "$ROOT" >/dev/null
docker build -q -f images/postgres.Dockerfile -t "$POSTGRES_IMAGE" . >/dev/null

step "secrets (plain files standing in for 1Password)"
mkdir -p "$RAPTOR_RUN/keys" "$RAPTOR_TLS"
pw() { openssl rand -hex 16; }
PANEL_PW=$(pw)
cat > "$RAPTOR_RUN/postgres.env" <<ENV
POSTGRES_PASSWORD=$(pw)
PANEL_DB_PASSWORD=$PANEL_PW
REPLICATOR_PASSWORD=$(pw)
PGBACKREST_REPO1_S3_BUCKET=raptor-backups
PGBACKREST_REPO1_S3_ENDPOINT=minio
PGBACKREST_REPO1_STORAGE_PORT=9000
PGBACKREST_REPO1_S3_REGION=us-east-1
PGBACKREST_REPO1_S3_KEY=rehearsal
PGBACKREST_REPO1_S3_KEY_SECRET=rehearsal-secret
PGBACKREST_REPO1_CIPHER_PASS=$(pw)
PGBACKREST_REPO1_STORAGE_VERIFY_TLS=n
ENV
cat > "$RAPTOR_RUN/panel.env" <<ENV
PANEL_DATABASE_URL=postgres://panel:$PANEL_PW@postgres:5432/raptor?sslmode=disable
PANEL_MAIL_LOG=1
ENV
(cd "$ROOT" && go run ./cmd/panel keygen "$RAPTOR_RUN/keys/signing.key" >/dev/null && go run ./cmd/panel keygen "$RAPTOR_RUN/keys/data.key" >/dev/null)
chmod 0644 "$RAPTOR_RUN/keys/"*.key "$RAPTOR_RUN/"*.env 2>/dev/null || true
# The Panel insists its keys are private: owned by its user, mode 0600.
docker run --rm -v "$RAPTOR_RUN/keys:/k" alpine sh -c 'chown 65532:65532 /k/*.key && chmod 0600 /k/*.key'

step "TLS: an origin certificate and a stand-in for Cloudflare's origin-pull CA"
cd "$RAPTOR_TLS"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj "/CN=$API_HOST" \
  -addext "subjectAltName=DNS:$API_HOST" -keyout origin-key.pem -out origin.pem 2>/dev/null
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj "/CN=Fake Origin Pull CA" \
  -keyout pull-ca-key.pem -out origin-pull-ca.pem 2>/dev/null
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -subj "/CN=cloudflare" -keyout cf-key.pem -out cf.csr 2>/dev/null
openssl x509 -req -in cf.csr -CA origin-pull-ca.pem -CAkey pull-ca-key.pem -CAcreateserial -days 2 -out cf.pem 2>/dev/null
chmod 0644 ./*.pem
cd - >/dev/null

step "object storage (MinIO, standing in for the backup bucket)"
docker network create "$NET" >/dev/null
mkdir -p "$W/minio-certs"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 -subj "/CN=minio" \
  -addext "subjectAltName=DNS:minio" -keyout "$W/minio-certs/private.key" -out "$W/minio-certs/public.crt" 2>/dev/null
chmod 0644 "$W/minio-certs/"*
docker run -d --name raptor-rehearsal-minio --network "$NET" --network-alias minio \
  -e MINIO_ROOT_USER=rehearsal -e MINIO_ROOT_PASSWORD=rehearsal-secret \
  -v "$W/minio-certs:/certs:ro" cgr.dev/chainguard/minio server --certs-dir /certs /tmp/data >/dev/null
for _ in $(seq 1 30); do
  docker run --rm --network "$NET" --entrypoint sh cgr.dev/chainguard/minio-client:latest-dev -c \
    'mc --insecure alias set m https://minio:9000 rehearsal rehearsal-secret >/dev/null 2>&1 && mc --insecure mb -p m/raptor-backups >/dev/null 2>&1' && break
  sleep 1
done

step "Postgres"
$P up -d --wait postgres
pglog=$($P logs postgres 2>&1)
grep -q "backup repository ready" <<<"$pglog" || fail "the first start didn't make the backup repository"
if grep -q "server does not shut down" <<<"$pglog"; then fail "the first start waited on the WAL archive"; fi
if grep -q "Peer authentication failed" <<<"$pglog"; then fail "the health check logs failed logins"; fi
echo "ok: first start made the backup repository, without waiting or failed logins"
# Again by hand, as DEPLOY.md does: harmless once it exists.
$P exec -T -u postgres postgres pgbackrest --stanza=raptor stanza-create
$P exec -T -u postgres postgres pgbackrest --stanza=raptor check

step "migrations, then two Panels behind Caddy"
$P run --rm --no-deps panel-a migrate
$P up -d --wait panel-a panel-b caddy
curl_cf() { curl -sS --max-time 5 --resolve "$API_HOST:$HTTPS_PORT:127.0.0.1" --cacert "$RAPTOR_TLS/origin.pem" \
  --cert "$RAPTOR_TLS/cf.pem" --key "$RAPTOR_TLS/cf-key.pem" "$@"; }
[ "$(curl_cf -o /dev/null -w '%{http_code}' "https://$API_HOST:$HTTPS_PORT/healthz")" = 200 ] || fail "the Panel didn't answer through Caddy"
# Each Panel's own route, which transfer connections take.
for p in panel-a panel-b; do
  [ "$(curl_cf -o /dev/null -w '%{http_code}' "https://$API_HOST:$HTTPS_PORT/i/$p/healthz")" = 200 ] || fail "$p's route through Caddy didn't reach it"
done
methods=$(curl_cf -H "Origin: $PANEL_APP_URL" -H 'Content-Type: application/json' -d '{}' \
  "https://$API_HOST:$HTTPS_PORT/api/raptor.panel.v1.AuthService/GetSignInMethods")
echo "sign-in methods: $methods"
echo "$methods" | grep -q '"email":true' || fail "the API didn't answer through Caddy"
if curl -sS --max-time 5 --resolve "$API_HOST:$HTTPS_PORT:127.0.0.1" --cacert "$RAPTOR_TLS/origin.pem" \
  -o /dev/null "https://$API_HOST:$HTTPS_PORT/healthz" 2>/dev/null; then
  fail "Caddy answered without Cloudflare's client certificate"
fi
echo "ok: only Cloudflare (its client certificate) gets in"

pq() { $P exec -T -u postgres postgres psql -qAt -d raptor -c "$1"; }
pq "INSERT INTO orgs (name) VALUES ('rehearsal')" >/dev/null
# The Panel reports the archive's health (pg_monitor).
pq "SET ROLE panel; SELECT sum(size) FROM pg_ls_waldir()" >/dev/null || fail "the Panel's role can't see the WAL"

step "backups: a full backup to object storage, then a restore test"
scripts/backup.sh full | tail -n 12
scripts/restore-test.sh

step "a rolling deploy, while something asks the Panel every 100 ms"
probe=$W/probe.log
( while :; do curl_cf -o /dev/null -w '%{http_code}\n' "https://$API_HOST:$HTTPS_PORT/healthz" 2>/dev/null || echo fail; sleep 0.1; done ) >"$probe" &
prober=$!
PANEL_REPO=raptor-panel scripts/deploy.sh rehearsal
sleep 1
kill "$prober"
total=$(wc -l <"$probe" | tr -d ' ')
bad=$(grep -cv '^200$' "$probe" || true)
echo "probe: $total requests during the deploy, $bad not answered"
[ "$total" -gt 50 ] || fail "the probe barely ran"
[ "$bad" = 0 ] || fail "the Panel stopped answering during the deploy"

step "the server is gone: rebuild it from the backups"
# A write after the last backup, archived (at most a minute later in
# production, archive_timeout): the rebuild must bring it back too.
pq "INSERT INTO orgs (name) VALUES ('after the backup')" >/dev/null
seg=$(pq "SELECT pg_walfile_name(pg_switch_wal())")
for _ in $(seq 1 60); do
  [ "$(pq "SELECT coalesce(last_archived_wal >= '$seg', false) FROM pg_stat_archiver")" = t ] && break
  sleep 1
done
before=$(pq "SELECT count(*) FROM orgs")
$P down -v >/dev/null 2>&1
rm -f panel/.env
scripts/restore.sh
PANEL_REPO=raptor-panel scripts/deploy.sh rehearsal
$P up -d --wait caddy
methods=$(curl_cf -H "Origin: $PANEL_APP_URL" -H 'Content-Type: application/json' -d '{}' \
  "https://$API_HOST:$HTTPS_PORT/api/raptor.panel.v1.AuthService/GetSignInMethods")
echo "$methods" | grep -q '"email":true' || fail "the rebuilt server's Panels didn't answer"
after=$(pq "SELECT count(*) FROM orgs")
[ "$after" = "$before" ] || fail "the rebuilt database has $after orgs, the old one had $before"
[ "$(pq "SELECT count(*) FROM orgs WHERE name = 'after the backup'")" = 1 ] || fail "the write after the last backup was lost"
pq "INSERT INTO orgs (name) VALUES ('after the rebuild')" >/dev/null || fail "the rebuilt database doesn't take writes"
echo "ok: rebuilt with all $before orgs, the one written after the backup included, and it takes writes"
scripts/backup.sh diff | tail -n 4
echo "ok: the rebuilt server backs up to the same archive"

printf '\nREHEARSAL OK: Caddy with client certs, two Panels, WAL archive and backup, restore test, zero-downtime deploy, rebuild from the backups with nothing lost, backups after it\n'
