-- name: CreateSupportRequest :one
INSERT INTO support_requests (org_id, node_id, staff_id, staff_email, level, minutes, reason, ticket)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: GetSupportRequest :one
SELECT * FROM support_requests WHERE id = $1;

-- name: NodeSupportRequests :many
SELECT * FROM support_requests WHERE node_id = $1 ORDER BY created_at DESC LIMIT 20;

-- name: StaffSupportRequests :many
SELECT r.*, n.name AS node_name, o.name AS org_name
FROM support_requests r JOIN nodes n ON n.id = r.node_id JOIN orgs o ON o.id = r.org_id
ORDER BY r.created_at DESC LIMIT 100;

-- name: ApproveSupportRequest :execrows
UPDATE support_requests SET status = 'approved', decided_at = now(), decided_by = $2, granted_level = $3, expires_at = $4
WHERE id = $1 AND node_id = $5 AND status = 'pending';

-- name: DenySupportRequest :execrows
UPDATE support_requests SET status = 'denied', decided_at = now(), decided_by = $2
WHERE id = $1 AND org_id = $3 AND status = 'pending';

-- name: RevokeSupportRequest :execrows
UPDATE support_requests SET status = 'revoked' WHERE id = $1 AND node_id = $2 AND status = 'approved';

-- name: RevokeNodeSupportRequests :execrows
UPDATE support_requests SET status = 'revoked' WHERE node_id = $1 AND status = 'approved' AND expires_at > now();

-- name: OrgAdminEmails :many
SELECT u.email FROM org_members m JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 AND m.role IN ('owner', 'admin');

-- name: NodeNameOrg :one
SELECT n.name, n.org_id, o.name AS org_name FROM nodes n JOIN orgs o ON o.id = n.org_id
WHERE n.id = $1 AND n.deleted_at IS NULL;
