-- +goose Up
-- The address each node connects from, for its hostname
-- (n-<short_id>.<node domain>, docs/ARCHITECTURE.md#node-dns).
ALTER TABLE nodes ADD COLUMN public_ipv4 inet;
ALTER TABLE nodes ADD COLUMN public_ipv6 inet;
-- What DNS has, so a record is only written when the address changes.
ALTER TABLE nodes ADD COLUMN dns_ipv4 inet;
ALTER TABLE nodes ADD COLUMN dns_ipv6 inet;

-- +goose Down
ALTER TABLE nodes DROP COLUMN dns_ipv6;
ALTER TABLE nodes DROP COLUMN dns_ipv4;
ALTER TABLE nodes DROP COLUMN public_ipv6;
ALTER TABLE nodes DROP COLUMN public_ipv4;
