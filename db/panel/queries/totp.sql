-- name: SaveTOTPSetup :exec
INSERT INTO totp_setups (user_id, secret, expires_at) VALUES ($1, $2, $3)
ON CONFLICT (user_id) DO UPDATE SET secret = excluded.secret, expires_at = excluded.expires_at;

-- name: TakeTOTPSetup :one
SELECT * FROM totp_setups WHERE user_id = $1 FOR UPDATE;

-- name: DeleteTOTPSetup :exec
DELETE FROM totp_setups WHERE user_id = $1;

-- name: EnableTOTP :exec
UPDATE users SET totp_secret = $2, totp_enabled_at = now(), totp_last_step = $3 WHERE id = $1;

-- name: DisableTOTP :exec
UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = 0 WHERE id = $1;

-- name: LockUserTOTP :one
-- The user's TOTP state, locked so a code's time step is used once.
SELECT totp_secret, totp_last_step FROM users WHERE id = $1 FOR UPDATE;

-- name: SetTOTPStep :exec
UPDATE users SET totp_last_step = $2 WHERE id = $1;

-- name: DeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = $1;

-- name: AddRecoveryCode :exec
INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2);

-- name: UseRecoveryCode :execrows
UPDATE recovery_codes SET used_at = now() WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL;

-- name: CountRecoveryCodes :one
SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL;

-- name: CreatePendingSignin :exec
INSERT INTO pending_signins (user_id, token_hash, expires_at) VALUES ($1, $2, $3);

-- name: PendingSigninByToken :one
SELECT * FROM pending_signins WHERE token_hash = $1 FOR UPDATE;

-- name: CountPendingSigninAttempt :exec
UPDATE pending_signins SET attempts = attempts + 1 WHERE id = $1;

-- name: DeletePendingSignin :exec
DELETE FROM pending_signins WHERE id = $1;

-- name: PrunePending :exec
DELETE FROM pending_signins WHERE expires_at < now();

-- name: PruneTOTPSetups :exec
DELETE FROM totp_setups WHERE expires_at < now();
