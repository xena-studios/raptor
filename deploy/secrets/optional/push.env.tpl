# Push notifications to Raptor's phone app (docs/DEPLOY.md#push-notifications).
# Optional: skipped until the item "Push notifications" exists in the vault
# (scripts/secrets.sh); phones can't register for push without it.
PANEL_APNS_KEY={{ op://Raptor production/Push notifications/apns_key }}
PANEL_APNS_KEY_ID={{ op://Raptor production/Push notifications/apns_key_id }}
PANEL_APNS_TEAM_ID={{ op://Raptor production/Push notifications/apns_team_id }}
PANEL_FCM_CREDENTIALS={{ op://Raptor production/Push notifications/fcm_credentials }}
