-- +goose Up
-- Re-importing an egg from a link makes that link its source
-- (docs/PANEL.md#imported-eggs, DECISIONS #340).
GRANT UPDATE (source_url) ON eggs TO raptor_app;

-- +goose Down
REVOKE UPDATE (source_url) ON eggs FROM raptor_app;
