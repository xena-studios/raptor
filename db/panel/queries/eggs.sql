-- The egg library (docs/PANEL.md#eggs). Org eggs go through row-level
-- security as the user; official ones (org_id NULL) through the Panel's
-- own connection, for staff.

-- name: CreateEgg :one
INSERT INTO eggs (org_id, name, summary, category, origin, status, egg, sha256, source_url, parent_id, parent_name, created_by)
VALUES (@org_id, @name, @summary, @category, @origin, @status, @egg, @sha256, @source_url, @parent_id, @parent_name, @created_by)
RETURNING *;

-- name: AddEggVersion :exec
INSERT INTO egg_versions (egg_id, version, egg, sha256, note, created_by)
VALUES (@egg_id, @version, @egg, @sha256, @note, @created_by);

-- name: CountOrgEggs :one
SELECT count(*) FROM eggs WHERE org_id = $1;

-- name: CountOfficialEggs :one
SELECT count(*) FROM eggs WHERE org_id IS NULL;

-- name: ListLibraryEggs :many
-- An org's eggs, and the official ones it can see.
SELECT sqlc.embed(e), u.email AS created_by_email
FROM eggs e LEFT JOIN users u ON u.id = e.created_by
WHERE e.org_id = @org_id OR (e.org_id IS NULL AND e.status = 'published')
ORDER BY e.org_id NULLS LAST, e.name, e.id;

-- name: ListOfficialEggs :many
SELECT sqlc.embed(e), u.email AS created_by_email
FROM eggs e LEFT JOIN users u ON u.id = e.created_by
WHERE e.org_id IS NULL ORDER BY e.name, e.id;

-- name: GetEgg :one
SELECT sqlc.embed(e), u.email AS created_by_email
FROM eggs e LEFT JOIN users u ON u.id = e.created_by
WHERE e.id = $1;

-- name: GetEggForUpdate :one
SELECT * FROM eggs WHERE id = $1 FOR UPDATE;

-- name: UpdateEgg :exec
UPDATE eggs SET name = @name, summary = @summary, category = @category, egg = @egg, sha256 = @sha256,
    version = @version, updated_at = now()
WHERE id = @id;

-- name: SetEggStatus :execrows
-- Only published eggs are shared: unpublishing takes one out of the
-- community too.
UPDATE eggs SET status = @status, updated_at = now(),
    shared_at = CASE WHEN @status::text = 'published' THEN shared_at END
WHERE id = @id;

-- name: DeleteEgg :execrows
DELETE FROM eggs WHERE id = $1;

-- name: ListEggVersions :many
SELECT version, sha256, note, created_by, created_at FROM egg_versions
WHERE egg_id = $1 ORDER BY version DESC LIMIT 100;

-- name: GetEggVersion :one
SELECT * FROM egg_versions WHERE egg_id = $1 AND version = $2;

-- name: OrgEggExists :one
SELECT EXISTS (SELECT 1 FROM eggs WHERE org_id = $1 AND sha256 = $2);

-- name: EggServerCounts :many
-- How many servers (on any node) were made from each egg, by the
-- egg_source the web app sends: a library egg's ID (or "org:" and the ID,
-- before the library), or a built-in egg's.
SELECT CASE WHEN egg_source LIKE 'org:%' THEN substr(egg_source, 5) ELSE egg_source END::text AS egg,
    count(*)::int AS n
FROM m_servers WHERE egg_source <> '' GROUP BY 1;

-- name: OrgEggServerCounts :many
SELECT CASE WHEN s.egg_source LIKE 'org:%' THEN substr(s.egg_source, 5) ELSE s.egg_source END::text AS egg,
    count(*)::int AS n
FROM m_servers s JOIN nodes n ON n.id = s.node_id
WHERE n.org_id = $1 AND s.egg_source <> '' GROUP BY 1;

-- name: OfficialEggExists :one
SELECT EXISTS (SELECT 1 FROM eggs WHERE org_id IS NULL AND sha256 = $1);

-- Community eggs (docs/PANEL.md#community-eggs).

-- name: SetEggShared :execrows
UPDATE eggs SET shared_at = CASE WHEN @shared::boolean THEN coalesce(shared_at, now()) END
WHERE id = @id;

-- name: ListCommunityEggs :many
-- Shared eggs, by how many orgs added them or by newest; on the Panel's
-- own connection (the publishers' org names), so it filters itself.
SELECT sqlc.embed(e), o.name AS org_name,
    (SELECT count(*) FROM eggs c WHERE c.parent_id = e.id::text)::int AS copies
FROM eggs e JOIN orgs o ON o.id = e.org_id
WHERE e.shared_at IS NOT NULL
  AND (@include_removed::boolean OR (e.removed_at IS NULL AND e.status = 'published'))
  AND (@query::text = '' OR e.name ILIKE '%' || @query || '%' OR e.summary ILIKE '%' || @query || '%')
  AND (@category::text = '' OR e.category = @category)
ORDER BY CASE WHEN @newest::boolean THEN 0 ELSE (SELECT count(*) FROM eggs c WHERE c.parent_id = e.id::text) END DESC,
    e.shared_at DESC, e.id
LIMIT @lim OFFSET @off;

-- name: EggPublisher :one
SELECT o.name, (SELECT count(*) FROM eggs c WHERE c.parent_id = e.id::text)::int AS copies
FROM eggs e LEFT JOIN orgs o ON o.id = e.org_id WHERE e.id = $1;

-- name: AddEggReport :execrows
INSERT INTO egg_reports (egg_id, reporter_id, reason, details)
VALUES (@egg_id, @reporter_id, @reason, @details)
ON CONFLICT DO NOTHING;

-- name: ListOpenEggReports :many
SELECT r.id, r.egg_id, r.reason, r.details, r.created_at, coalesce(u.email, '')::text AS reporter_email,
    e.name AS egg_name, coalesce(o.name, '')::text AS org_name, e.removed_at
FROM egg_reports r JOIN eggs e ON e.id = r.egg_id
LEFT JOIN users u ON u.id = r.reporter_id LEFT JOIN orgs o ON o.id = e.org_id
WHERE r.resolved_at IS NULL ORDER BY r.created_at LIMIT 200;

-- name: CountOpenEggReports :one
SELECT count(*)::int FROM egg_reports WHERE egg_id = $1 AND resolved_at IS NULL;

-- name: RemoveCommunityEgg :execrows
UPDATE eggs SET removed_at = now(), removed_reason = @reason, removed_by = @removed_by
WHERE id = @id AND shared_at IS NOT NULL AND removed_at IS NULL;

-- name: RestoreCommunityEgg :execrows
UPDATE eggs SET removed_at = NULL, removed_reason = '', removed_by = ''
WHERE id = @id AND removed_at IS NOT NULL;

-- name: ResolveEggReports :execrows
UPDATE egg_reports SET resolved_at = now(), resolved_by = @resolved_by, resolution = @resolution
WHERE egg_id = @egg_id AND resolved_at IS NULL;
