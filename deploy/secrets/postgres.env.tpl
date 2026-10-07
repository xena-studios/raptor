# Postgres and pgBackRest secrets (scripts/secrets.sh → /run/raptor/postgres.env).
POSTGRES_PASSWORD={{ op://Raptor production/Postgres/superuser_password }}
PANEL_DB_PASSWORD={{ op://Raptor production/Postgres/panel_password }}
REPLICATOR_PASSWORD={{ op://Raptor production/Postgres/replicator_password }}
PGBACKREST_REPO1_S3_BUCKET={{ op://Raptor production/Backup storage/bucket }}
PGBACKREST_REPO1_S3_ENDPOINT={{ op://Raptor production/Backup storage/endpoint }}
PGBACKREST_REPO1_S3_REGION={{ op://Raptor production/Backup storage/region }}
PGBACKREST_REPO1_S3_KEY={{ op://Raptor production/Backup storage/access_key_id }}
PGBACKREST_REPO1_S3_KEY_SECRET={{ op://Raptor production/Backup storage/secret_access_key }}
PGBACKREST_REPO1_CIPHER_PASS={{ op://Raptor production/Backup storage/encryption_passphrase }}
