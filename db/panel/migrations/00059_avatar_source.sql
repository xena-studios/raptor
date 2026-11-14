-- +goose Up
-- Where an account's picture came from (docs/PANEL.md#images): signing in
-- with Google, GitHub, or Discord copies the picture there once, only if
-- the account never had one ('') — never over one that was uploaded, and
-- never again after it was removed.
ALTER TABLE users ADD COLUMN avatar_source text NOT NULL DEFAULT ''
    CHECK (avatar_source IN ('', 'upload', 'removed', 'google', 'github', 'discord'));
UPDATE users SET avatar_source = 'upload' WHERE avatar IS NOT NULL;

-- +goose Down
ALTER TABLE users DROP COLUMN avatar_source;
