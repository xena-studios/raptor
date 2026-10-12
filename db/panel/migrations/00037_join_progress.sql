-- +goose Up
-- Live progress while a node links (docs/ARCHITECTURE.md#enrollment):
-- `raptor bootstrap` reports each step with the join token, and the Add
-- Node page shows them. Each entry: {"name", "state", "detail", "at"}.
ALTER TABLE join_tokens ADD COLUMN progress jsonb NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE join_tokens DROP COLUMN progress;
