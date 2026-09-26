-- +goose Up

-- Durable jobs (installs now; backups, schedules later). docs/WINGS.md#jobs
CREATE TABLE jobs (
    id           TEXT PRIMARY KEY,
    server_id    TEXT NOT NULL DEFAULT '',
    type         TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 1,
    run_after    INTEGER NOT NULL, -- unix ms
    error        TEXT NOT NULL DEFAULT '',
    result       TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL, -- unix ms
    started_at   INTEGER,
    finished_at  INTEGER
) STRICT;

CREATE INDEX jobs_queue ON jobs (status, run_after);
CREATE INDEX jobs_server ON jobs (server_id, created_at);

-- Event outbox: every change on the node, numbered. The Panel catches up
-- from its last acknowledged seq. AUTOINCREMENT: a seq is never reused, even
-- after pruning. docs/ARCHITECTURE.md#mirror-sync
CREATE TABLE events (
    seq       INTEGER PRIMARY KEY AUTOINCREMENT,
    type      TEXT NOT NULL,
    server_id TEXT NOT NULL DEFAULT '',
    version   INTEGER NOT NULL DEFAULT 0,
    at        INTEGER NOT NULL, -- unix ms
    data      TEXT NOT NULL DEFAULT '{}'
) STRICT;

-- Commands from the Panel, for idempotency and replay protection.
-- docs/SECURITY-MODEL.md#passkey-signed-commands
CREATE TABLE executed_commands (
    command_id   TEXT PRIMARY KEY,
    payload_hash BLOB NOT NULL,
    action       TEXT NOT NULL,
    user_id      TEXT NOT NULL DEFAULT '',
    signed_by    BLOB, -- the passkey credential ID, for signed commands
    status       TEXT NOT NULL CHECK (status IN ('running', 'succeeded', 'failed')),
    result       TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    received_at  INTEGER NOT NULL, -- unix ms
    finished_at  INTEGER
) STRICT;

CREATE INDEX executed_commands_received ON executed_commands (received_at);

-- Passkeys this node trusts to sign dangerous commands. Owners' keys can sign
-- everything; delegates only their scope. Changed only by signed commands
-- (or root on the box). docs/SECURITY-MODEL.md#passkey-signed-commands
CREATE TABLE trusted_keys (
    credential_id BLOB PRIMARY KEY,
    user_id       TEXT NOT NULL,
    public_key    BLOB NOT NULL, -- COSE_Key
    role          TEXT NOT NULL CHECK (role IN ('owner', 'delegate')),
    server_id     TEXT NOT NULL DEFAULT '', -- delegate scope; '' = every server
    actions       TEXT NOT NULL DEFAULT '[]', -- delegate scope (JSON list)
    name          TEXT NOT NULL DEFAULT '',
    sign_count    INTEGER NOT NULL DEFAULT 0,
    expires_at    INTEGER, -- unix ms; delegates may expire
    added_by      BLOB, -- the credential that signed the addition; NULL = pinned locally
    added_at      INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE trusted_keys;
DROP TABLE executed_commands;
DROP TABLE events;
DROP TABLE jobs;
