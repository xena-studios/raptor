#!/bin/bash
# Fills in /run/raptor from 1Password (docs/DEPLOY.md#secrets): env files and
# the Panel's keys, in memory only, readable by root and the Panel's user
# (65532, distroless's nonroot). Run at boot (raptor-stack.service) and by
# deploy.sh. Needs the op CLI and a service account token, readable only by
# root, in /etc/raptor/op-token.
set -euo pipefail
cd "$(dirname "$0")/.."
OP_SERVICE_ACCOUNT_TOKEN=$(cat /etc/raptor/op-token)
export OP_SERVICE_ACCOUNT_TOKEN
VAULT="Raptor production"
umask 077
install -d -m 0750 -o root -g 65532 /run/raptor /run/raptor/keys
op inject --force -i secrets/panel.env.tpl -o /run/raptor/panel.env
op inject --force -i secrets/postgres.env.tpl -o /run/raptor/postgres.env
op read "op://$VAULT/Panel signing key/key" > /run/raptor/keys/signing.key
op read "op://$VAULT/Panel data key/key" > /run/raptor/keys/data.key
# The Panel refuses keys others can read: 0600, owned by its user.
chown 65532:65532 /run/raptor/keys/*.key /run/raptor/panel.env
chmod 0600 /run/raptor/keys/*.key /run/raptor/panel.env /run/raptor/postgres.env
echo "secrets: /run/raptor ready"
