-- +goose Up
-- Support access requests (docs/PANEL.md#support-access): staff ask, the
-- node's owner approves with their passkey (signing the grant Wings keeps)
-- or denies. The request's ID is the grant's. Wings is the authority on
-- whether a grant is in force; this is the Panel's record of it.
CREATE TABLE support_requests (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    node_id     uuid        NOT NULL REFERENCES nodes (id),
    staff_id    uuid        NOT NULL REFERENCES users (id),
    staff_email text        NOT NULL,
    level       int         NOT NULL CHECK (level BETWEEN 1 AND 3),
    minutes     int         NOT NULL CHECK (minutes BETWEEN 15 AND 10080),
    reason      text        NOT NULL,
    ticket      text        NOT NULL,
    status      text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'revoked')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    decided_at  timestamptz,
    decided_by  uuid,
    -- What the owner approved, which may be less than asked.
    granted_level int,
    expires_at  timestamptz
);
CREATE INDEX support_requests_node ON support_requests (node_id, created_at DESC);

-- +goose Down
DROP TABLE support_requests;
