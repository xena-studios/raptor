-- +goose Up
-- Support access (docs/PANEL.md#support-access): what the node's owner let
-- a Raptor staff member do, signed with the owner's passkey, until when.
-- Commands from staff name a grant here; Wings checks them against it.
CREATE TABLE support_grants (
    id          TEXT PRIMARY KEY,
    staff_id    TEXT NOT NULL,
    staff_email TEXT NOT NULL,
    level       INTEGER NOT NULL CHECK (level BETWEEN 1 AND 3),
    reason      TEXT NOT NULL,
    ticket      TEXT NOT NULL,
    granted_by  TEXT NOT NULL, -- the user whose passkey signed it
    created_at  INTEGER NOT NULL, -- unix ms
    expires_at  INTEGER NOT NULL,
    revoked_at  INTEGER,
    revoked_by  TEXT NOT NULL DEFAULT ''
) STRICT;

-- +goose Down
DROP TABLE support_grants;
