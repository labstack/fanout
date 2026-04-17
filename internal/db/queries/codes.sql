-- name: CreateVerificationCode :exec
INSERT INTO verification_codes (id, email, code_hash, expires_at)
VALUES (?, ?, ?, ?);

-- name: GetLatestUnusedCode :one
SELECT id, code_hash, attempts, used, expires_at
FROM verification_codes
WHERE email = ? AND used = 0
ORDER BY created_at DESC
LIMIT 1;

-- name: IncrementCodeAttempts :exec
UPDATE verification_codes SET attempts = attempts + 1 WHERE id = ?;

-- name: MarkCodeUsed :exec
UPDATE verification_codes SET used = 1 WHERE id = ?;

-- name: CleanupExpiredCodes :exec
DELETE FROM verification_codes WHERE expires_at < ?;
