-- +goose Up
-- Account settings (docs/PANEL.md#auth): the theme follows the account to
-- every browser, and deleting an account keeps its orgs' audit log (the
-- rows lose who they were about; actor_id still says who did it).
ALTER TABLE users ADD COLUMN theme text NOT NULL DEFAULT 'system'
    CHECK (theme IN ('system', 'light', 'dark'));

ALTER TABLE audit_log DROP CONSTRAINT audit_log_user_id_fkey;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE audit_log DROP CONSTRAINT audit_log_user_id_fkey;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;

ALTER TABLE users DROP COLUMN theme;
