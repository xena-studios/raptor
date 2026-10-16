-- +goose Up
-- The machine's own hostname and time zone, as the Panel last read them
-- (docs/PANEL.md#hostname-and-time-zone). The hostname follows the node's
-- name unless an admin set one on the System tab (hostname_custom).
ALTER TABLE nodes
    -- What the machine reported ('' until it's been read).
    ADD COLUMN hostname        text NOT NULL DEFAULT '' CHECK (length(hostname) <= 253),
    ADD COLUMN timezone        text NOT NULL DEFAULT '' CHECK (length(timezone) <= 64),
    -- '' follows the node's name; otherwise the hostname an admin set.
    ADD COLUMN hostname_custom text NOT NULL DEFAULT '' CHECK (length(hostname_custom) <= 253),
    -- The hostname the Panel last asked for, and the node's answer if it
    -- refused: it isn't asked again until what it should be changes.
    ADD COLUMN hostname_tried  text NOT NULL DEFAULT '',
    ADD COLUMN hostname_error  text NOT NULL DEFAULT '',
    -- When the machine's settings were last read; a new connection clears
    -- it, so they're read again.
    ADD COLUMN system_read_at  timestamptz;

-- +goose Down
ALTER TABLE nodes DROP COLUMN hostname, DROP COLUMN timezone, DROP COLUMN hostname_custom,
    DROP COLUMN hostname_tried, DROP COLUMN hostname_error, DROP COLUMN system_read_at;
