-- name: GetConfig :one
SELECT * FROM config WHERE group_key = ?;

-- name: UpsertConfig :one
INSERT INTO config (group_key, overrides, updated_at, updated_by, last_reason)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(group_key) DO UPDATE SET
  overrides = excluded.overrides,
  updated_at = excluded.updated_at,
  updated_by = excluded.updated_by,
  last_reason = excluded.last_reason
RETURNING *;

-- name: DeleteConfig :exec
DELETE FROM config WHERE group_key = ?;
