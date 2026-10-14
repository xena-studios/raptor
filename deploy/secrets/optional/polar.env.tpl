# Billing through Polar (docs/DEPLOY.md#billing). Optional: skipped until
# the item "Polar" exists in the vault (scripts/secrets.sh); billing is off
# without it.
PANEL_POLAR_TOKEN={{ op://Raptor production/Polar/token }}
PANEL_POLAR_PRODUCT_ID={{ op://Raptor production/Polar/product_id }}
PANEL_POLAR_WEBHOOK_SECRET={{ op://Raptor production/Polar/webhook_secret }}
