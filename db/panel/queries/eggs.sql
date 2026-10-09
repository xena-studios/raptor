-- name: CreateOrgEgg :one
INSERT INTO org_eggs (org_id, name, egg, sha256, source_url, imported_by)
VALUES (@org_id, @name, @egg, @sha256, @source_url, @imported_by)
ON CONFLICT (org_id, sha256) DO NOTHING
RETURNING *;

-- name: CountOrgEggs :one
SELECT count(*) FROM org_eggs WHERE org_id = $1;

-- name: ListOrgEggs :many
SELECT o.*, u.email AS imported_by_email
FROM org_eggs o LEFT JOIN users u ON u.id = o.imported_by
WHERE o.org_id = $1 ORDER BY o.name, o.id;

-- name: GetOrgEgg :one
SELECT * FROM org_eggs WHERE org_id = $1 AND id = $2;

-- name: DeleteOrgEgg :execrows
DELETE FROM org_eggs WHERE org_id = $1 AND id = $2;

-- name: OrgEggExists :one
SELECT EXISTS (SELECT 1 FROM org_eggs WHERE org_id = $1 AND sha256 = $2);
