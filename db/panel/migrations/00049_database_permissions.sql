-- +goose Up
-- databases.read and databases.write: a server's databases (docs/DATABASES.md#permissions).
ALTER TABLE server_grants DROP CONSTRAINT server_grants_permissions_check;
ALTER TABLE server_grants ADD CONSTRAINT server_grants_permissions_check CHECK (
    cardinality(permissions) > 0 AND permissions <@ ARRAY[
        'console.read', 'console.write', 'power', 'files.read', 'files.write', 'backups',
        'schedules', 'startup', 'reinstall', 'sftp', 'databases.read', 'databases.write'
    ]::text[]
);

-- +goose Down
UPDATE server_grants SET permissions = array_remove(array_remove(permissions, 'databases.read'), 'databases.write');
DELETE FROM server_grants WHERE cardinality(permissions) = 0;
ALTER TABLE server_grants DROP CONSTRAINT server_grants_permissions_check;
ALTER TABLE server_grants ADD CONSTRAINT server_grants_permissions_check CHECK (
    cardinality(permissions) > 0 AND permissions <@ ARRAY[
        'console.read', 'console.write', 'power', 'files.read', 'files.write', 'backups',
        'schedules', 'startup', 'reinstall', 'sftp'
    ]::text[]
);
