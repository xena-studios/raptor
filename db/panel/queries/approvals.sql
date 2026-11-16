-- name: AddPhoneApprover :exec
INSERT INTO phone_approvers (session_id, user_id, secret) VALUES ($1, $2, $3);

-- name: PhoneApprover :one
SELECT * FROM phone_approvers WHERE session_id = $1;

-- name: CountPhoneApprovers :one
SELECT count(*) FROM phone_approvers WHERE user_id = $1;

-- name: RemovePhoneApprover :execrows
DELETE FROM phone_approvers WHERE session_id = $1;

-- name: RemoveUserPhoneApprovers :exec
DELETE FROM phone_approvers WHERE user_id = $1;

-- name: TouchPhoneApprover :exec
UPDATE phone_approvers SET last_used_at = now() WHERE session_id = $1;

-- name: PhoneApproverSessions :many
-- The app sessions that approve for a user, to send a request to.
SELECT a.session_id FROM phone_approvers a JOIN sessions s ON s.id = a.session_id
WHERE a.user_id = $1 AND s.revoked_at IS NULL AND s.expires_at > now();

-- name: CreatePhoneApproval :one
INSERT INTO phone_approvals (user_id, purpose, pending_signin_id, session_id, number, choices, challenge, ip, user_agent, expires_at)
VALUES ($1, $2, sqlc.narg(pending_signin_id), sqlc.narg(session_id), $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: PhoneApproval :one
SELECT * FROM phone_approvals WHERE id = $1;

-- name: LockPhoneApproval :one
SELECT * FROM phone_approvals WHERE id = $1 FOR UPDATE;

-- name: DecidePhoneApproval :exec
UPDATE phone_approvals SET status = $2, decided_at = now() WHERE id = $1 AND status = 'pending';

-- name: UsePhoneApproval :execrows
-- An approval works once.
UPDATE phone_approvals SET status = 'used' WHERE id = $1 AND status = 'approved';

-- name: PendingPhoneApprovals :many
SELECT * FROM phone_approvals WHERE user_id = $1 AND status = 'pending' AND expires_at > now()
ORDER BY created_at DESC LIMIT 5;

-- name: CancelPendingApprovals :exec
-- A new request replaces the ones before it, for the same sign-in or session.
UPDATE phone_approvals SET status = 'denied', decided_at = now()
WHERE status = 'pending' AND (pending_signin_id = sqlc.narg(pending_signin_id) OR session_id = sqlc.narg(session_id));

-- name: PrunePhoneApprovals :exec
DELETE FROM phone_approvals WHERE created_at < now() - interval '1 day';
