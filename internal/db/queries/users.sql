-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: GetUserByKeyHash :one
SELECT * FROM users WHERE key_hash = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CreateUser :one
INSERT INTO users (id, email, name, role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateUser :one
UPDATE users SET email = ?, name = ?, role = ?, active = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteUser :execresult
DELETE FROM users WHERE id = ?;

-- name: TouchLogin :exec
UPDATE users SET logged_in_at = ?, updated_at = ? WHERE id = ?;

-- name: SetAPIKeyHash :exec
UPDATE users SET key_hash = ?, updated_at = ? WHERE id = ?;

-- name: RevokeAPIKey :exec
UPDATE users SET key_hash = NULL, updated_at = ? WHERE id = ?;
