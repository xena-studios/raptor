-- +goose Up
-- What each node's Wings said it understands when it last connected
-- (nodelink.Hello's capabilities), so the Panel only relies on what the
-- node can do: e.g. SFTP usernames without a server ID (sftp-username).
ALTER TABLE nodes ADD COLUMN capabilities text[] NOT NULL DEFAULT '{}';

-- SFTP usernames are t-<10> alone now; the server ID was only ever added
-- when the login was shown.
COMMENT ON COLUMN sftp_passwords.username IS 'The whole SFTP username (t-…); nodes without sftp-username get it as t-….<server short ID>.';

-- +goose Down
ALTER TABLE nodes DROP COLUMN capabilities;
