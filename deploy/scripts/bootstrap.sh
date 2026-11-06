#!/bin/bash
# Turns a fresh Debian or Ubuntu server (x86-64 or arm64, any provider) into
# the Panel's server (docs/DEPLOY.md#the-server), in one go:
#   git clone https://github.com/xena-studios/raptor.git /opt/raptor
#   /opt/raptor/deploy/scripts/bootstrap.sh restore v1.2.3   # from the backups
#   /opt/raptor/deploy/scripts/bootstrap.sh new v1.2.3       # an empty database
# A restore can stop at a moment instead of the end of the archive (before a
# bad migration, say), with --target-time "2026-11-05 12:00:00+00"; --yes
# skips the question about the old server.
# As root. It asks for the 1Password service account token once; everything
# else comes from the vault. Safe to run again after a failure: each step
# skips what's already done, except that a restore refuses a server that
# already has a database.
set -euo pipefail

# Everything runs from a function, so bash has read the whole script before
# the checkout below replaces this file.
main() {
MODE=${1:-}
TAG=${2:-}
YES=
restore_args=()
shift 2 || true
while [ $# -gt 0 ]; do
  case $1 in
  --yes) YES=--yes ;;
  --target-time) restore_args=(--type=time "--target=${2:?--target-time needs a time}" --target-action=promote); shift ;;
  *) MODE= ;;
  esac
  shift
done
if [[ $MODE != new && $MODE != restore ]] || ! [[ $TAG =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: bootstrap.sh new|restore vX.Y.Z [--yes] [--target-time TIME]" >&2
  exit 2
fi
[ "$(id -u)" = 0 ] || { echo "bootstrap: run as root" >&2; exit 1; }
REPO=/opt/raptor
[ -d "$REPO/.git" ] || { echo "bootstrap: clone the repository to $REPO first" >&2; exit 1; }
# shellcheck source=/dev/null
. /etc/os-release
[[ $ID == debian || $ID == ubuntu ]] || { echo "bootstrap: Debian or Ubuntu only (this is $ID)" >&2; exit 1; }
step() { printf '\n== %s\n' "$*"; }
export DEBIAN_FRONTEND=noninteractive

if [ "$MODE" = restore ] && [ "$YES" != --yes ]; then
  cat <<'WARN'
A restore makes this server the one writing to the backup repository. If
the old server's Postgres is still running, both write to it and the backups
are ruined. Stop it first (docker compose stop, in deploy/panel) or power the
old server off from its provider's console.
WARN
  read -rp "Is the old server's Postgres stopped or gone? Type yes: " answer </dev/tty
  [ "$answer" = yes ] || exit 1
fi

# Oracle's images ship iptables rules (netfilter-persistent) that reject
# everything but SSH and fight with ufw and Docker. Gone before Docker is
# installed, so its own chains aren't flushed with them. ufw takes over
# below; SSH is never blocked in between (the policies stay ACCEPT).
if systemctl is-enabled netfilter-persistent >/dev/null 2>&1; then
  step "removing the image's own iptables rules (netfilter-persistent)"
  systemctl disable --now netfilter-persistent
  rm -f /etc/iptables/rules.v4 /etc/iptables/rules.v6
  for t in iptables ip6tables; do $t -F INPUT; $t -F FORWARD; done
  if systemctl is-active docker >/dev/null 2>&1; then systemctl restart docker; fi
fi

step "packages: Docker, the 1Password CLI, ufw, automatic security updates"
arch=$(dpkg --print-architecture)
apt-get update -q
apt-get install -yq ca-certificates curl git gnupg ufw unattended-upgrades jq
install -m 0755 -d /etc/apt/keyrings
if [ ! -f /etc/apt/sources.list.d/docker.list ]; then
  curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o /etc/apt/keyrings/docker.asc
  echo "deb [arch=$arch signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/$ID $VERSION_CODENAME stable" \
    >/etc/apt/sources.list.d/docker.list
fi
if [ ! -f /etc/apt/sources.list.d/1password.list ]; then
  curl -fsSL https://downloads.1password.com/linux/keys/1password.asc | gpg --dearmor --yes -o /usr/share/keyrings/1password-archive-keyring.gpg
  echo "deb [arch=$arch signed-by=/usr/share/keyrings/1password-archive-keyring.gpg] https://downloads.1password.com/linux/debian/$arch stable main" \
    >/etc/apt/sources.list.d/1password.list
fi
apt-get update -q
apt-get install -yq docker-ce docker-ce-cli containerd.io docker-compose-plugin 1password-cli
cat >/etc/apt/apt.conf.d/20auto-upgrades <<'APT'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT
# Keys only, whatever the image allowed.
cat >/etc/ssh/sshd_config.d/10-raptor.conf <<'SSH'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
SSH
systemctl reload ssh 2>/dev/null || systemctl reload sshd

step "the deploy files, at $TAG"
git -C "$REPO" fetch -q --force --tags origin main
git -C "$REPO" checkout -q --detach "refs/tags/$TAG"

step "secrets from 1Password"
install -d -m 0700 /etc/raptor
if [ ! -s /etc/raptor/op-token ]; then
  read -rsp "1Password service account token (Raptor production, read-only): " token </dev/tty
  echo
  [ -n "$token" ] || { echo "bootstrap: no token given" >&2; exit 1; }
  install -m 0600 /dev/stdin /etc/raptor/op-token <<<"$token"
  unset token
fi
"$REPO/deploy/scripts/secrets.sh"

step "the deploy user (releases deploy themselves over SSH: docs/DEPLOY.md#automatic-deploys)"
id deploy >/dev/null 2>&1 || useradd --system --create-home --shell /bin/bash deploy
echo "deploy ALL=(root) NOPASSWD: $REPO/deploy/scripts/deploy-from-ci.sh *" >/etc/sudoers.d/raptor-deploy
chmod 0440 /etc/sudoers.d/raptor-deploy
visudo -cq
OP_SERVICE_ACCOUNT_TOKEN=$(cat /etc/raptor/op-token)
export OP_SERVICE_ACCOUNT_TOKEN
if pub=$(op read "op://Raptor production/Deploy SSH key/public_key" 2>/dev/null); then
  install -d -m 0700 -o deploy -g deploy /home/deploy/.ssh
  # shellcheck disable=SC2016 # \$SSH_ORIGINAL_COMMAND is for sshd to expand, not us
  printf 'restrict,command="sudo %s/deploy/scripts/deploy-from-ci.sh \\"$SSH_ORIGINAL_COMMAND\\"" %s\n' "$REPO" "$pub" \
    | install -m 0600 -o deploy -g deploy /dev/stdin /home/deploy/.ssh/authorized_keys
else
  echo "bootstrap: no \"Deploy SSH key\" (public_key) in 1Password; automatic deploys won't reach this server until it's added and this runs again" >&2
fi
unset OP_SERVICE_ACCOUNT_TOKEN

step "the firewall and the boot units"
cp "$REPO"/deploy/systemd/raptor-*.service "$REPO"/deploy/systemd/raptor-*.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable raptor-stack.service
systemctl enable --now raptor-firewall.service
systemctl restart raptor-firewall.service

cd "$REPO/deploy/panel"
if [ "$MODE" = new ]; then
  step "an empty database and a new backup repository"
  docker compose up -d --wait postgres
  docker compose exec -T -u postgres postgres pgbackrest --stanza=raptor stanza-create
  docker compose exec -T -u postgres postgres pgbackrest --stanza=raptor check
else
  step "the database, from the backup repository"
  # Run again after a later step failed: the restore is done, carry on.
  if [ "$(docker compose exec -T -u postgres postgres psql -qAt -d raptor -c 'SELECT NOT pg_is_in_recovery()' 2>/dev/null)" = t ]; then
    echo "bootstrap: the database is already restored and running; going on"
  else
    ../scripts/restore.sh "${restore_args[@]}"
  fi
fi

step "the Panel, $TAG"
../scripts/deploy.sh "$TAG"
docker compose up -d caddy

step "backups"
systemctl enable --now raptor-backup.timer raptor-restore-test.timer raptor-disk-check.timer
../scripts/backup.sh full | tail -n 5

ip=$(curl -fsS -4 https://1.1.1.1/cdn-cgi/trace | sed -n 's/^ip=//p')
hostkey=$(cut -d' ' -f1,2 /etc/ssh/ssh_host_ed25519_key.pub)
cat <<DONE

== Done. This server runs the Panel $TAG at $ip.
Last steps, from your Mac:
  1. Cloudflare, raptorpanel.net, DNS: point "api" (A, proxied) at $ip,
     and delete its AAAA record if this server has no IPv6.
     Then: curl https://api.raptorpanel.net/healthz
  2. GitHub, so releases deploy here:
       gh secret set DEPLOY_HOST --env production --body '$ip'
       gh secret set DEPLOY_KNOWN_HOSTS --env production --body '$ip $hostkey'
  3. Optional: the backup and restore-test ping URLs in /etc/raptor/stack.env
     (docs/DEPLOY.md#monitoring).
DONE
}

main "$@"
exit
