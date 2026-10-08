-- +goose Up
-- Account settings (docs/PANEL.md#auth): the theme follows the account to
-- every browser, deleting an account keeps its orgs' audit log (the rows
-- lose who they were about; actor_id still says who did it), and email
-- codes can confirm a new address.
ALTER TABLE users ADD COLUMN theme text NOT NULL DEFAULT 'system'
    CHECK (theme IN ('system', 'light', 'dark'));

ALTER TABLE audit_log DROP CONSTRAINT audit_log_user_id_fkey;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL;

-- A code confirming a new address is for one account: its ID follows.
ALTER TABLE email_codes DROP CONSTRAINT email_codes_purpose;
ALTER TABLE email_codes ADD CONSTRAINT email_codes_purpose
    CHECK (purpose IN ('signin', 'reauth') OR purpose ~ '^email_change:[0-9a-f-]{36}$');

-- +goose Down
DELETE FROM email_codes WHERE purpose LIKE 'email_change:%';
ALTER TABLE email_codes DROP CONSTRAINT email_codes_purpose;
ALTER TABLE email_codes ADD CONSTRAINT email_codes_purpose CHECK (purpose IN ('signin', 'reauth'));

ALTER TABLE audit_log DROP CONSTRAINT audit_log_user_id_fkey;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE;

ALTER TABLE users DROP COLUMN theme;
