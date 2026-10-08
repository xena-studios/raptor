-- +goose Up
-- console.read: watching a server's console, which members can be given
-- without being able to send commands (console.write includes it).
ALTER TABLE server_grants DROP CONSTRAINT server_grants_permissions_check;
ALTER TABLE server_grants ADD CONSTRAINT server_grants_permissions_check CHECK (
    cardinality(permissions) > 0 AND permissions <@ ARRAY[
        'console.read', 'console.write', 'power', 'files.read', 'files.write', 'backups',
        'schedules', 'startup', 'reinstall', 'sftp'
    ]::text[]
);

-- +goose Down
DELETE FROM server_grants WHERE permissions = ARRAY['console.read'];
UPDATE server_grants SET permissions = array_remove(permissions, 'console.read');
ALTER TABLE server_grants DROP CONSTRAINT server_grants_permissions_check;
ALTER TABLE server_grants ADD CONSTRAINT server_grants_permissions_check CHECK (
    cardinality(permissions) > 0 AND permissions <@ ARRAY[
        'console.write', 'power', 'files.read', 'files.write', 'backups',
        'schedules', 'startup', 'reinstall', 'sftp'
    ]::text[]
);
