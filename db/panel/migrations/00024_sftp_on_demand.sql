-- +goose Up
-- A node's SFTP port opens only while someone has a temporary password on
-- it (docs/DECISIONS.md #225); admins can stop SFTP on a node altogether.
-- sftp_enabled stays what the node last reported.
ALTER TABLE nodes ADD COLUMN sftp_allowed boolean NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE nodes DROP COLUMN sftp_allowed;
