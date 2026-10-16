-- +goose Up
-- Owners can delete an org with no nodes left (docs/PANEL.md#deleting-an-org):
-- its members, invitations, and join tokens go, and the row stays, marked,
-- for its audit log and billing history.
ALTER TABLE orgs ADD COLUMN deleted_at timestamptz;

-- +goose Down
ALTER TABLE orgs DROP COLUMN deleted_at;
