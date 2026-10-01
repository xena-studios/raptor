-- +goose Up
-- Signed dangerous actions, from the node's own records (`raptor audit`,
-- docs/SECURITY-MODEL.md#passkey-signed-commands): every command that needed
-- a passkey signature, run or rejected, and local key resets. Kept for a
-- year, unlike executed_commands (a week, for idempotency).
CREATE TABLE audit_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    at            INTEGER NOT NULL, -- unix ms
    command_id    TEXT NOT NULL DEFAULT '',
    action        TEXT NOT NULL,
    server_id     TEXT NOT NULL DEFAULT '',
    user_id       TEXT NOT NULL DEFAULT '', -- Panel user, or local:<unix user>
    credential_id BLOB,                     -- the passkey that signed it
    key_name      TEXT NOT NULL DEFAULT '',
    command_hash  BLOB,                     -- what the passkey signed
    outcome       TEXT NOT NULL CHECK (outcome IN ('running', 'ok', 'failed', 'rejected')),
    detail        TEXT NOT NULL DEFAULT '' -- the error, or why it was rejected
) STRICT;

CREATE INDEX audit_log_at ON audit_log (at);
CREATE INDEX audit_log_command ON audit_log (command_id);

-- +goose Down
DROP TABLE audit_log;
