-- +goose Up
-- players: a Minecraft server's whitelist, operators, bans, and kicks
-- (#422), without the console or the files. Grants that can already send
-- console commands could do all of it, so they get it too.
ALTER TABLE server_grants DROP CONSTRAINT server_grants_permissions_check;
ALTER TABLE server_grants ADD CONSTRAINT server_grants_permissions_check CHECK (
    cardinality(permissions) > 0 AND permissions <@ ARRAY[
        'console.read', 'console.write', 'power', 'files.read', 'files.write', 'backups',
        'schedules', 'startup', 'reinstall', 'sftp', 'databases.read', 'databases.write', 'players'
    ]::text[]
);
UPDATE server_grants SET permissions = array_append(permissions, 'players')
WHERE 'console.write' = ANY(permissions) AND NOT 'players' = ANY(permissions);

-- +goose Down
UPDATE server_grants SET permissions = array_remove(permissions, 'players');
DELETE FROM server_grants WHERE cardinality(permissions) = 0;
ALTER TABLE server_grants DROP CONSTRAINT server_grants_permissions_check;
ALTER TABLE server_grants ADD CONSTRAINT server_grants_permissions_check CHECK (
    cardinality(permissions) > 0 AND permissions <@ ARRAY[
        'console.read', 'console.write', 'power', 'files.read', 'files.write', 'backups',
        'schedules', 'startup', 'reinstall', 'sftp', 'databases.read', 'databases.write'
    ]::text[]
);
