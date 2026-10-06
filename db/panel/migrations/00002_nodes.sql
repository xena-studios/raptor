-- +goose Up
-- Orgs own nodes. Users, members, and roles come with accounts (3.2).
CREATE TABLE orgs (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Nodes (docs/ARCHITECTURE.md#enrollment). Rows are never deleted, only
-- marked, so a short ID (n-<short_id>.raptornodes.net) is never reused.
CREATE TABLE nodes (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id           uuid        NOT NULL REFERENCES orgs (id),
    name             text        NOT NULL,
    short_id         text        NOT NULL UNIQUE CHECK (short_id ~ '^[a-z0-9]{8}$'),
    public_key       bytea       NOT NULL CHECK (length(public_key) = 32),
    facts            jsonb       NOT NULL DEFAULT '{}',
    wings_version    text        NOT NULL DEFAULT '',
    protocol_version int         NOT NULL DEFAULT 0,
    last_seen_at     timestamptz,
    last_acked_seq   bigint      NOT NULL DEFAULT 0,
    key_revoked_at   timestamptz,
    deleted_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX nodes_org ON nodes (org_id) WHERE deleted_at IS NULL;

-- Join tokens: single use, an hour, bound to an org; stored hashed. node_id
-- records which node used it, so an enrollment whose answer was lost can be
-- repeated with the same token and key.
CREATE TABLE join_tokens (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id     uuid        NOT NULL REFERENCES orgs (id),
    token_hash bytea       NOT NULL UNIQUE,
    name       text        NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    node_id    uuid        REFERENCES nodes (id),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE join_tokens;
DROP TABLE nodes;
DROP TABLE orgs;
