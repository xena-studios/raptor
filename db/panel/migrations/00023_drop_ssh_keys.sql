-- +goose Up
-- Nothing, on purpose. SSH keys are gone (docs/DECISIONS.md #223), but
-- v0.1.0 reads users.sftp_username on every session lookup, and migrations
-- run while the old version is still serving (docs/DEPLOY.md). The table
-- and the column are dropped in the release after v0.2.0, once nothing
-- running uses them (ROADMAP: "Next up").
SELECT 1;

-- +goose Down
SELECT 1;
