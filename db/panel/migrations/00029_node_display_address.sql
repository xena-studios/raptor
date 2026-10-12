-- +goose Up
-- What players should connect to instead of the node's own hostname: a
-- proxy's domain (TCPShield and the like), or the owner's own name for the
-- box, so the machine's address isn't on screen (docs/PANEL.md#node-addresses).
-- '' shows the hostname. "host" or "host:port"; with a port, it's shown as
-- is, without the server's.
ALTER TABLE nodes ADD COLUMN display_address text NOT NULL DEFAULT ''
    CHECK (length(display_address) <= 260);

-- +goose Down
ALTER TABLE nodes DROP COLUMN display_address;
