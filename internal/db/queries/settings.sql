-- name: GetSetting :one
SELECT * FROM settings WHERE key = ?;

-- name: UpsertSetting :one
INSERT INTO settings (key, value, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
  value = excluded.value,
  updated_at = excluded.updated_at
RETURNING *;

-- name: DeleteSetting :exec
DELETE FROM settings WHERE key = ?;
