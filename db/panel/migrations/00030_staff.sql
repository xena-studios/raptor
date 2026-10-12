-- +goose Up
-- Raptor's own staff (docs/PANEL.md#staff): platform admins and support.
-- Granted only with `panel staff add` on the Panel's server, never from the
-- web, so a staff account that's taken over can't make more.
CREATE TABLE staff (
    user_id    uuid        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    role       text        NOT NULL CHECK (role IN ('admin', 'support')),
    added_at   timestamptz NOT NULL DEFAULT now(),
    added_by   text        NOT NULL
);

-- Everything staff do. Staff can't change it: rows can only be added.
CREATE TABLE staff_audit (
    id          uuid        PRIMARY KEY DEFAULT uuidv7(),
    at          timestamptz NOT NULL DEFAULT now(),
    staff_id    uuid        NOT NULL,
    staff_email text        NOT NULL,
    action      text        NOT NULL,
    target_type text        NOT NULL DEFAULT '',
    target_id   text        NOT NULL DEFAULT '',
    meta        jsonb       NOT NULL DEFAULT '{}',
    ip          inet
);
CREATE INDEX staff_audit_at ON staff_audit (at DESC);

-- +goose StatementBegin
CREATE FUNCTION staff_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'staff_audit is append-only';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER staff_audit_append_only BEFORE UPDATE OR DELETE OR TRUNCATE ON staff_audit
    FOR EACH STATEMENT EXECUTE FUNCTION staff_audit_append_only();

-- When the session last proved a passkey (signing in or confirming with
-- one): staff pages need one recently, staff changes very recently.
ALTER TABLE sessions ADD COLUMN passkey_at timestamptz;

-- Suspended accounts can't sign in; suspended orgs can't be used in the
-- Panel (their nodes and servers keep running).
ALTER TABLE users ADD COLUMN suspended_at timestamptz;
ALTER TABLE users ADD COLUMN suspended_reason text NOT NULL DEFAULT '';
ALTER TABLE orgs ADD COLUMN suspended_at timestamptz;
ALTER TABLE orgs ADD COLUMN suspended_reason text NOT NULL DEFAULT '';

-- Platform counts, every hour, for the staff overview's graphs.
CREATE TABLE platform_stats (
    at              timestamptz PRIMARY KEY,
    users           int NOT NULL,
    orgs            int NOT NULL,
    nodes           int NOT NULL,
    nodes_connected int NOT NULL,
    servers         int NOT NULL,
    servers_running int NOT NULL,
    storage_bytes   bigint NOT NULL
);

-- +goose Down
DROP TABLE platform_stats;
ALTER TABLE orgs DROP COLUMN suspended_reason;
ALTER TABLE orgs DROP COLUMN suspended_at;
ALTER TABLE users DROP COLUMN suspended_reason;
ALTER TABLE users DROP COLUMN suspended_at;
ALTER TABLE sessions DROP COLUMN passkey_at;
DROP TABLE staff_audit;
DROP FUNCTION staff_audit_append_only();
DROP TABLE staff;
