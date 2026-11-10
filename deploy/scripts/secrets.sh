#!/bin/bash
# Fills in /run/raptor from 1Password (docs/DEPLOY.md#secrets): env files,
# the Panel's keys, and Caddy's certificate, in memory only, readable by root and the Panel's user
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
install -d -m 0700 /run/raptor/tls
op inject --force -i secrets/panel.env.tpl -o /run/raptor/panel.env
# Features that can wait: each is added once its item is in the vault, and
# skipped with a warning until then. An item that's there but wrong still
# fails here.
for tpl in secrets/optional/*.env.tpl; do
  item=$(grep -om1 "op://$VAULT/[^/]*" "$tpl" | cut -d/ -f4)
  if ! op item get "$item" --vault "$VAULT" >/dev/null 2>&1; then
    echo "secrets: no \"$item\" in 1Password, skipping $(basename "$tpl" .env.tpl)" >&2
    continue
  fi
  op inject -i "$tpl" >>/run/raptor/panel.env
done
op inject --force -i secrets/postgres.env.tpl -o /run/raptor/postgres.env
op read "op://$VAULT/Panel signing key/key" > /run/raptor/keys/signing.key
op read "op://$VAULT/Panel data key/key" > /run/raptor/keys/data.key
# Caddy's origin certificate, its key, and Cloudflare's origin pull CA
# (docs/DEPLOY.md#secrets), each from the vault's "Origin certificate" item,
# or, on a server set up before they were all there, from /etc/raptor/tls.
# A file in neither place is an error: Caddy can't start without it.
tls_file() { # field file
  if op read "op://$VAULT/Origin certificate/$1" >"/run/raptor/tls/$2" 2>/dev/null && [ -s "/run/raptor/tls/$2" ]; then
    return
  fi
  if [ -s "/etc/raptor/tls/$2" ]; then
    echo "secrets: no $1 in \"Origin certificate\" in 1Password, using /etc/raptor/tls/$2" >&2
    cp "/etc/raptor/tls/$2" "/run/raptor/tls/$2"
    return
  fi
  echo "secrets: no $1 in \"Origin certificate\" in 1Password, and no /etc/raptor/tls/$2 (docs/DEPLOY.md#secrets)" >&2
  exit 1
}
tls_file certificate origin.pem
tls_file private_key origin-key.pem
tls_file origin_pull_ca origin-pull-ca.pem
# A file that's there but won't parse (a PEM pasted into a 1Password
# Password field comes back on one line) would leave Caddy restarting
# forever; stop here instead, with the field to fix.
tls_check() { # what field command...
  if ! "${@:3}" >/dev/null 2>&1; then
    echo "secrets: $1 from \"Origin certificate\" ($2) isn't valid PEM: paste it whole into a Text field, not a Password one (docs/DEPLOY.md#secrets)" >&2
    exit 1
  fi
}
tls_check "the certificate" certificate openssl x509 -noout -in /run/raptor/tls/origin.pem
tls_check "the private key" private_key openssl pkey -noout -in /run/raptor/tls/origin-key.pem
tls_check "the origin pull CA" origin_pull_ca openssl x509 -noout -in /run/raptor/tls/origin-pull-ca.pem
if [ "$(openssl x509 -noout -pubkey -in /run/raptor/tls/origin.pem | sha256sum)" != "$(openssl pkey -pubout -in /run/raptor/tls/origin-key.pem | sha256sum)" ]; then
  echo "secrets: the private key in \"Origin certificate\" isn't the certificate's" >&2
  exit 1
fi
# The Panel refuses keys others can read: 0600, owned by its user.
chown 65532:65532 /run/raptor/keys/*.key /run/raptor/panel.env
chmod 0600 /run/raptor/keys/*.key /run/raptor/panel.env /run/raptor/postgres.env
echo "secrets: /run/raptor ready"
