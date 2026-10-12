-- Not a migration: sqlc reads this after db/panel/migrations, so the
-- generated code no longer reads users.sftp_username while the column is
-- still there for v0.2.0, which does (docs/DEPLOY.md: drop in a later
-- release). In the release after v0.3.0 this becomes a migration and the
-- file goes (docs/ROADMAP.md).
ALTER TABLE users DROP COLUMN sftp_username;
