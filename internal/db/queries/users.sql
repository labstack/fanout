-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = ?;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CountActiveAdmins :one
SELECT COUNT(*) FROM users WHERE role = 'admin' AND active = 1;

-- name: CreateUser :one
INSERT INTO users (id, email, name, role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateUser :one
UPDATE users SET email = ?, name = ?, role = ?, active = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: IncrementUserAuthVersion :exec
UPDATE users SET auth_version = auth_version + 1, updated_at = ? WHERE id = ?;

-- name: DeleteUser :execresult
DELETE FROM users WHERE id = ?;

-- name: TouchLogin :exec
UPDATE users SET logged_in_at = ?, updated_at = ? WHERE id = ?;
