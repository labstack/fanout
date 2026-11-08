-- name: InsertAlertEmailDelivery :exec
INSERT INTO alert_email_deliveries (
    id, notification_id, rule_id, alert_id, recipient, content, next_attempt_at, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListDueAlertEmailDeliveries :many
SELECT * FROM alert_email_deliveries
WHERE status = 'pending' AND next_attempt_at <= ?
ORDER BY next_attempt_at, id
LIMIT ?;

-- name: ListOpenAlertEmailDeliveries :many
SELECT * FROM alert_email_deliveries WHERE status IN ('pending', 'sending') ORDER BY id;

-- name: ListAlertEmailDeliveriesByNotification :many
SELECT * FROM alert_email_deliveries WHERE notification_id = ? ORDER BY id;

-- name: ClaimAlertEmailDelivery :exec
UPDATE alert_email_deliveries SET status = 'sending', attempts = attempts + 1 WHERE id = ?;

-- name: FinishAlertEmailDelivery :exec
UPDATE alert_email_deliveries SET status = ?, last_error = ?, sent_at = ?, next_attempt_at = ? WHERE id = ?;

-- name: CountAlertEmailsSentSince :one
SELECT COUNT(*) FROM alert_email_deliveries WHERE recipient = ? AND status = 'sent' AND sent_at >= ?;

-- name: PruneAlertEmailDeliveries :execresult
DELETE FROM alert_email_deliveries WHERE status IN ('sent', 'failed', 'suppressed') AND created_at < ?;

-- name: UpdateRuleEmailStatus :exec
UPDATE alert_rules SET email_status = ?, email_status_detail = ?, email_status_at = ? WHERE id = ?;
