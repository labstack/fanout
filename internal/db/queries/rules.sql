-- name: GetRule :one
SELECT * FROM alert_rules WHERE id = ?;

-- name: ListRules :many
SELECT * FROM alert_rules ORDER BY created_at DESC;

-- name: ListEnabledRules :many
SELECT * FROM alert_rules WHERE enabled = 1 ORDER BY created_at DESC;

-- name: CreateRule :one
INSERT INTO alert_rules (
    id, name, description, enabled, service, namespace, expression,
    for_seconds, cooldown_s, repeat_interval_s, webhook_url,
    webhook_headers, webhook_template, notify_on_resolve
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateRule :one
UPDATE alert_rules SET
    name = ?, description = ?, enabled = ?, service = ?, namespace = ?,
    expression = ?, for_seconds = ?, cooldown_s = ?, repeat_interval_s = ?,
    webhook_url = ?, webhook_headers = ?, webhook_template = ?,
    notify_on_resolve = ?, updated_at = datetime('now')
WHERE id = ?
RETURNING *;

-- name: DeleteRule :exec
DELETE FROM alert_rules WHERE id = ?;
