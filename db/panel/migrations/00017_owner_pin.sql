-- +goose Up
-- The owner's passkey, signed when they made the join token (nodecmd.OwnerPin):
-- handed to the node when it links, which checks it and trusts the key.
ALTER TABLE join_tokens ADD COLUMN owner_pin jsonb;

-- +goose Down
ALTER TABLE join_tokens DROP COLUMN owner_pin;
