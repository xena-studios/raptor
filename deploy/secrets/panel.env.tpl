# The Panel's secrets, filled in from 1Password by scripts/secrets.sh into
# /run/raptor/panel.env (memory only, root and the Panel's user). The vault
# is "Raptor production"; see docs/DEPLOY.md#secrets for its items.
PANEL_DATABASE_URL=postgres://panel:{{ op://Raptor production/Postgres/panel_password }}@postgres:5432/raptor?sslmode=disable
PANEL_RESEND_API_KEY={{ op://Raptor production/Resend/api_key }}
PANEL_TURNSTILE_SECRET={{ op://Raptor production/Turnstile/secret_key }}
PANEL_GITHUB_CLIENT_ID={{ op://Raptor production/GitHub OAuth/client_id }}
PANEL_GITHUB_CLIENT_SECRET={{ op://Raptor production/GitHub OAuth/client_secret }}
