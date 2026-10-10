# Raptor Backup Storage (docs/PANEL.md#raptor-backup-storage): Raptor's B2
# bucket "raptor-backup-storage" and a master key that makes a key per node.
# Optional: skipped until the item "Raptor Backup Storage" exists in the
# vault (scripts/secrets.sh); without it the Panel doesn't offer it.
PANEL_BACKUP_STORAGE_KEY_ID={{ op://Raptor production/Raptor Backup Storage/key_id }}
PANEL_BACKUP_STORAGE_KEY={{ op://Raptor production/Raptor Backup Storage/application_key }}
PANEL_BACKUP_STORAGE_BUCKET_ID={{ op://Raptor production/Raptor Backup Storage/bucket_id }}
PANEL_BACKUP_STORAGE_ENDPOINT={{ op://Raptor production/Raptor Backup Storage/endpoint }}
PANEL_BACKUP_STORAGE_REGION={{ op://Raptor production/Raptor Backup Storage/region }}
