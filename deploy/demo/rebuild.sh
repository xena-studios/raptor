#!/usr/bin/env bash
# Rebuilds Raptor's public demo node from scratch (#449,
# docs/DEPLOY.md#rebuilding-the-demo-node): a fresh OS, Raptor installed and
# linked with `raptor bootstrap` and a join token from the demo org, then
# `raptor demo on` with the node's demo key. Run it from an operator's
# machine, monthly, or whenever the node may be compromised.
#
# It doesn't assume a provider. Reinstalling the OS is a step you plug in:
# set DEMO_REINSTALL_CMD to a command that reinstalls the machine (it gets
# the host as its argument and should return once the provider started the
# reinstall), or leave it unset and the script tells you to do it in the
# provider's panel (Contabo: Your services > the VPS > Reinstall, Ubuntu
# 24.04, with your SSH key) and waits.
#
# The demo key comes from the Panel (`panel staff demo node <node-id>`),
# which needs the new node's ID, so the script stops to ask for it once the
# node is linked. Set DEMO_KEY_CMD to a command that prints it instead (it
# gets the node ID as its argument).
#
# Usage:
#   deploy/demo/rebuild.sh -host <address> -token <rpt_join_…> [-user root]
#     [-identity ~/.ssh/key] [-sandboxes 4] [-ports 30000-30099] [-templates 6]
set -euo pipefail

host="" user="root" token="" identity="" sandboxes=4 ports="30000-30099" templates=6
while [ $# -gt 0 ]; do
	case "$1" in
	-host) host="$2"; shift 2 ;;
	-user) user="$2"; shift 2 ;;
	-token) token="$2"; shift 2 ;;
	-identity) identity="$2"; shift 2 ;;
	-sandboxes) sandboxes="$2"; shift 2 ;;
	-ports) ports="$2"; shift 2 ;;
	-templates) templates="$2"; shift 2 ;;
	-h | -help | --help) sed -n '2,24p' "$0"; exit 0 ;;
	*) echo "rebuild: unknown argument $1" >&2; exit 2 ;;
	esac
done
[ -n "$host" ] || { echo "rebuild: -host is required" >&2; exit 2; }
case "$token" in
rpt_join_*) ;;
*) echo "rebuild: -token is a join token from the demo org's Add node page (rpt_join_…)" >&2; exit 2 ;;
esac
case "$ports" in
[0-9]*-[0-9]*) ;;
*) echo "rebuild: -ports is a range like 30000-30099" >&2; exit 2 ;;
esac

ssh_opts=(-o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new)
[ -n "$identity" ] && ssh_opts+=(-i "$identity")
# Commands run as root: directly, or with sudo for another user.
sudo_=""
[ "$user" = root ] || sudo_="sudo -n "
# shellcheck disable=SC2029 # the commands are meant to expand here
remote() { ssh "${ssh_opts[@]}" "$user@$host" "$sudo_$*"; }
# remote_script runs a script from stdin as root, with arguments.
# shellcheck disable=SC2029 # the arguments are meant to expand here
remote_script() { ssh "${ssh_opts[@]}" "$user@$host" "${sudo_}bash -s -- $*"; }
say() { printf '\n==> %s\n' "$*"; }

say "This wipes $host and everything on it: the showcase servers, the sandboxes, the warm templates."
read -r -p "Type the host again to go on: " again
[ "$again" = "$host" ] || { echo "rebuild: stopped" >&2; exit 1; }

# 1. A fresh OS, by the provider.
say "Reinstalling the OS"
if [ -n "${DEMO_REINSTALL_CMD:-}" ]; then
	$DEMO_REINSTALL_CMD "$host"
else
	cat <<MSG
Reinstall $host in the provider's panel now: Ubuntu 24.04 (or Debian 12),
with your SSH key for $user. On Contabo: Your services > the VPS >
Reinstall. The old host key changes, so remove it here afterwards:
  ssh-keygen -R $host
MSG
	read -r -p "Press Enter once the reinstall finished. " _
fi
ssh-keygen -R "$host" >/dev/null 2>&1 || true

# 2. Wait for SSH.
say "Waiting for SSH on $host"
for i in $(seq 1 90); do
	if remote true 2>/dev/null; then break; fi
	[ "$i" = 90 ] && { echo "rebuild: $host didn't answer on SSH for 15 minutes" >&2; exit 1; }
	sleep 10
done

# 3. Key-only SSH, and the OS up to date.
say "Hardening SSH and updating the OS"
remote_script <<'SH'
set -e
install -d -m 0755 /etc/ssh/sshd_config.d
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\n' > /etc/ssh/sshd_config.d/10-raptor-demo.conf
systemctl reload ssh 2>/dev/null || systemctl reload sshd
export DEBIAN_FRONTEND=noninteractive
apt-get update -q </dev/null && apt-get -y -q upgrade </dev/null && apt-get -y -q install curl ca-certificates unattended-upgrades </dev/null
SH

# 4. Raptor, linked to the demo org.
say "Installing Raptor and linking it with the join token"
remote_script "$token" <<'SH'
set -e
curl -fsSL https://get.raptorpanel.net | bash -s -- -token "$1" -yes
SH
node_id=$(remote "sed -n 's/^node_id: *\"\{0,1\}\([^\"]*\)\"\{0,1\}\$/\1/p' /etc/raptor/config.yml" | tr -d '[:space:]')
[ -n "$node_id" ] || { echo "rebuild: the node didn't link (no node_id in /etc/raptor/config.yml)" >&2; exit 1; }
echo "Linked as node $node_id."

# 5. The demo key: from the Panel, for this node only.
say "The node's demo key"
if [ -n "${DEMO_KEY_CMD:-}" ]; then
	key=$($DEMO_KEY_CMD "$node_id" | sed -n 's/.*raptor demo on -key \([^ ]*\).*/\1/p' | head -1)
else
	cat <<MSG
On the Panel's server, run:
  docker compose exec panel-a panel staff demo node $node_id -sandboxes $sandboxes -ports $ports -warm $templates
and paste the key it prints (after "raptor demo on -key").
MSG
	read -r -p "Demo key: " key
fi
[ -n "$key" ] || { echo "rebuild: no demo key" >&2; exit 1; }

say "Making it the demo node"
remote "raptor demo on -key '$key' -sandboxes $sandboxes -ports $ports -templates $templates"
sleep 5
remote "raptor demo status && raptor status"

cat <<MSG

==> Done on the node. What's left, from the Panel (docs/DEPLOY.md#rebuilding-the-demo-node):
  - Remove the old node from the demo org in the app (Nodes), and
    panel staff demo node-remove <old-node-id>.
  - Make the showcase servers again (or restore them from their backups'
    destination with Recover), set the Paper server's whitelist and
    permissions (docs/DEPLOY.md#public-demo), take and lock known-good
    backups, then panel staff demo server / backup for each.
  - The node's alert rules: Node CPU above 95% for 10 minutes and Node
    outbound traffic above 20 MB/s for 10 minutes, to the staff channel.
  - Check the status page's demo node check is green.
MSG
