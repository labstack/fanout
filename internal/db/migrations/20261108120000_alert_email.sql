-- +goose Up
-- email_recipients is a JSON array of addresses. It stays NULL on older rules,
-- which have no email destination of their own.
ALTER TABLE alert_rules ADD COLUMN email_recipients TEXT;
ALTER TABLE alert_rules ADD COLUMN email_status TEXT NOT NULL DEFAULT '';
ALTER TABLE alert_rules ADD COLUMN email_status_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE alert_rules ADD COLUMN email_status_at TEXT NOT NULL DEFAULT '';

-- One row per recipient of one notification, so a retry never mails a recipient
-- that already accepted it. Rows are written with the alert transition.
CREATE TABLE alert_email_deliveries (
    id              TEXT PRIMARY KEY,
    notification_id TEXT NOT NULL,
    rule_id         TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    alert_id        TEXT NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    recipient       TEXT NOT NULL,
    content         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    last_error      TEXT NOT NULL DEFAULT '',
    sent_at         TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);
CREATE INDEX alert_email_deliveries_due ON alert_email_deliveries (status, next_attempt_at);
CREATE INDEX alert_email_deliveries_sent ON alert_email_deliveries (recipient, sent_at) WHERE status = 'sent';
CREATE INDEX alert_email_deliveries_notification ON alert_email_deliveries (notification_id);
