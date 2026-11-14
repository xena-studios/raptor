-- name: SaveImage :one
-- Stores a picture, or refreshes one already stored. No row comes back for
-- a picture that was taken down.
INSERT INTO images (id, png, width, height) VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET created_at = now() WHERE images.taken_down_at IS NULL
RETURNING id;

-- name: ImagePNG :one
SELECT png FROM images WHERE id = $1 AND png IS NOT NULL;

-- name: PruneImages :exec
-- Pictures nothing uses any more, a day after they were last uploaded.
DELETE FROM images i
WHERE i.taken_down_at IS NULL AND i.created_at < now() - interval '1 day'
  AND NOT EXISTS (SELECT 1 FROM orgs WHERE logo = i.id)
  AND NOT EXISTS (SELECT 1 FROM users WHERE avatar = i.id);

-- name: TakeDownImage :execrows
-- Empties a picture for good: it's no longer served, nothing shows it, and
-- the same file can't be uploaded again.
WITH orgs_cleared AS (UPDATE orgs SET logo = NULL WHERE orgs.logo = @id::text),
     users_cleared AS (UPDATE users SET avatar = NULL WHERE users.avatar = @id::text)
UPDATE images SET png = NULL, taken_down_at = now() WHERE images.id = @id::text AND images.taken_down_at IS NULL;
