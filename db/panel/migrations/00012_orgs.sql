-- +goose Up
-- Org members and invitations (docs/PANEL.md#permissions). Every org keeps
-- at least one owner; the Panel enforces it.
CREATE TABLE org_members (
    org_id     uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text        NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX org_members_user ON org_members (user_id);

-- An invitation is for one address: only someone signed in with it can
-- accept. The link's token is stored hashed.
CREATE TABLE org_invitations (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id      uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    email       text        NOT NULL CHECK (email = lower(email)),
    role        text        NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    token_hash  bytea       NOT NULL UNIQUE,
    invited_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX org_invitations_org ON org_invitations (org_id) WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- +goose Down
DROP TABLE org_invitations;
DROP TABLE org_members;
