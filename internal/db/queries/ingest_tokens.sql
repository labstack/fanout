-- name: InsertIngestTokenWithinLimit :execrows
INSERT INTO ingest_tokens (id, name, token_hash, hint, source, created_at, created_by)
SELECT sqlc.arg(id), sqlc.arg(name), sqlc.arg(token_hash), sqlc.arg(hint), sqlc.arg(source), sqlc.arg(created_at), sqlc.narg(created_by)
WHERE (SELECT COUNT(*) FROM ingest_tokens AS active WHERE active.revoked_at IS NULL) < CAST(sqlc.arg(max_active) AS INTEGER);

-- name: InsertFirstIngestToken :execrows
INSERT INTO ingest_tokens (id, name, token_hash, hint, source, created_at, created_by)
SELECT sqlc.arg(id), sqlc.arg(name), sqlc.arg(token_hash), sqlc.arg(hint), sqlc.arg(source), sqlc.arg(created_at), sqlc.narg(created_by)
WHERE NOT EXISTS (SELECT 1 FROM ingest_tokens);

-- name: GetActiveIngestTokenIDByHash :one
SELECT id FROM ingest_tokens WHERE token_hash = ? AND revoked_at IS NULL;

-- name: ListActiveIngestTokens :many
SELECT id, name, hint, created_at, last_used_at
FROM ingest_tokens
WHERE revoked_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: HasActiveIngestToken :one
SELECT EXISTS (SELECT 1 FROM ingest_tokens WHERE revoked_at IS NULL);

-- name: RevokeIngestToken :one
UPDATE ingest_tokens
SET revoked_at = sqlc.arg(revoked_at), revoked_by = sqlc.narg(revoked_by)
WHERE id = sqlc.arg(id) AND revoked_at IS NULL
RETURNING id, name;

-- name: UpdateIngestTokenLastUsed :exec
UPDATE ingest_tokens SET last_used_at = sqlc.arg(last_used_at) WHERE id = sqlc.arg(id);
