-- name: GetAlert :one
SELECT * FROM alerts WHERE rule_id = ? AND service = ?;

-- name: ListAlerts :many
SELECT * FROM alerts ORDER BY created_at DESC;

-- name: ListAlertsByState :many
SELECT * FROM alerts WHERE state = ? ORDER BY created_at DESC;

-- name: ListAlertsByService :many
SELECT * FROM alerts WHERE service = ? ORDER BY created_at DESC;

-- name: ListAlertsByRuleID :many
SELECT * FROM alerts WHERE rule_id = ? ORDER BY created_at DESC;

-- name: UpsertAlert :one
INSERT INTO alerts (
    id, rule_id, service, state, value, fired_at, resolved_at,
    repeated_at, last_eval, last_delivery_status, last_delivery_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(rule_id, service) DO UPDATE SET
    state = excluded.state,
    value = excluded.value,
    fired_at = excluded.fired_at,
    resolved_at = excluded.resolved_at,
    repeated_at = excluded.repeated_at,
    last_eval = excluded.last_eval,
    last_delivery_status = excluded.last_delivery_status,
    last_delivery_at = excluded.last_delivery_at
RETURNING *;

-- name: DeleteAlert :exec
DELETE FROM alerts WHERE id = ?;

-- name: DeleteAlertsByRuleIDAndState :exec
DELETE FROM alerts WHERE rule_id = ? AND state IN (?, ?);

-- name: UpdateDeliveryStatus :exec
UPDATE alerts SET last_delivery_status = ?, last_delivery_at = ?
WHERE rule_id = ? AND service = ?;

-- name: AlertSummary :many
SELECT state, COUNT(*) AS count FROM alerts GROUP BY state;

-- name: PruneResolved :execresult
DELETE FROM alerts WHERE state = 'resolved' AND resolved_at < datetime('now', ? || ' days');
