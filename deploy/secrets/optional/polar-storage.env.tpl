# Raptor Backup Storage, charged through Polar's meter (docs/DEPLOY.md#billing).
# Optional: skipped until the item "Polar storage meter" exists in the vault
# (scripts/secrets.sh); storage past what's included isn't charged without
# it. Its own item, so adding it can't break the "Polar" item's template.
PANEL_POLAR_STORAGE_METER_ID={{ op://Raptor production/Polar storage meter/meter_id }}
