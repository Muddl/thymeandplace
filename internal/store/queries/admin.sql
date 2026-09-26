-- name: ListPasskeys :many
SELECT * FROM admin_passkey ORDER BY created_at, credential_id;

-- name: InsertPasskey :exec
INSERT INTO admin_passkey (credential_id, public_key, sign_count, credential, name, created_at)
VALUES (@credential_id, @public_key, @sign_count, @credential, @name, @created_at);

-- name: UpdatePasskeyUse :exec
UPDATE admin_passkey SET sign_count = @sign_count, credential = @credential, last_used_at = @last_used_at
WHERE credential_id = @credential_id;

-- name: RenamePasskey :execrows
UPDATE admin_passkey SET name = @name WHERE credential_id = @credential_id;

-- name: DeletePasskey :execrows
DELETE FROM admin_passkey WHERE credential_id = @credential_id;

-- name: CountPasskeys :one
SELECT count(*) FROM admin_passkey;

-- name: GetBackup :one
SELECT * FROM admin_backup WHERE id;

-- name: GetBackupForUpdate :one
SELECT * FROM admin_backup WHERE id FOR UPDATE;

-- name: SetBackup :exec
INSERT INTO admin_backup (id, password_hash, totp_secret_enc) VALUES (true, @password_hash, @totp_secret_enc)
ON CONFLICT (id) DO UPDATE SET
  password_hash = EXCLUDED.password_hash, totp_secret_enc = EXCLUDED.totp_secret_enc,
  last_totp_step = 0, failed_count = 0, lock_level = 0, locked_until = NULL;

-- name: UpdateBackupState :exec
UPDATE admin_backup SET failed_count = @failed_count, lock_level = @lock_level,
  locked_until = @locked_until, last_totp_step = @last_totp_step
WHERE id;

-- name: InsertSession :exec
INSERT INTO admin_session (token_hash, csrf_token, method, created_at, last_seen_at, auth_at)
VALUES (@token_hash, @csrf_token, @method, @now, @now, @now);

-- name: GetSession :one
SELECT * FROM admin_session WHERE token_hash = @token_hash;

-- name: TouchSession :exec
UPDATE admin_session SET last_seen_at = @now WHERE token_hash = @token_hash;

-- name: SetSessionAuthAt :exec
UPDATE admin_session SET auth_at = @now WHERE token_hash = @token_hash;

-- name: DeleteSession :exec
DELETE FROM admin_session WHERE token_hash = @token_hash;

-- name: DeleteAllSessions :execrows
DELETE FROM admin_session;

-- name: InsertEnrollmentToken :exec
INSERT INTO admin_enrollment_token (token_hash, created_at, expires_at) VALUES (@token_hash, @created_at, @expires_at);

-- name: EnrollmentTokenValid :one
SELECT EXISTS (
  SELECT 1 FROM admin_enrollment_token WHERE token_hash = @token_hash AND used_at IS NULL AND expires_at > @now
);

-- name: ConsumeEnrollmentToken :execrows
UPDATE admin_enrollment_token SET used_at = @now
WHERE token_hash = @token_hash AND used_at IS NULL AND expires_at > @now;

-- name: InsertLoginAttempt :exec
INSERT INTO admin_login_log (at, ip, method, outcome) VALUES (@at, @ip, @method, @outcome);

-- name: CountRecentLoginAttempts :one
SELECT count(*) FROM admin_login_log WHERE ip = @ip AND at > @since AND outcome <> 'rate_limited';
