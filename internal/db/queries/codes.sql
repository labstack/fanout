-- name: CreateVerificationCode :exec
INSERT INTO verifications (id, email, code_hash, purpose, expires_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetLatestUnusedCode :one
SELECT id, code_hash, attempts, used, expires_at
FROM verifications
WHERE email = ? AND purpose = ? AND used = 0
ORDER BY created_at DESC
LIMIT 1;

-- name: GetUnusedLoginLink :one
SELECT id, email, expires_at
FROM verifications
WHERE code_hash = ? AND purpose = 'login_link' AND used = 0
ORDER BY created_at DESC
LIMIT 1;

-- name: IncrementCodeAttempts :exec
UPDATE verifications SET attempts = attempts + 1 WHERE id = ?;

-- name: MarkCodeUsed :exec
UPDATE verifications SET used = 1 WHERE id = ?;

-- name: CleanupExpiredCodes :exec
DELETE FROM verifications WHERE expires_at < ?;
