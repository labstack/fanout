-- +goose Up
-- Each firing episode keeps its own row, so history shows every episode.
-- Only one pending or firing alert may exist per rule and service.
CREATE TABLE alerts_next (
    id                   TEXT PRIMARY KEY,
    rule_id              TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    service              TEXT NOT NULL,
    state                TEXT NOT NULL,
    value                REAL DEFAULT 0,
    fired_at             TEXT DEFAULT '',
    resolved_at          TEXT DEFAULT '',
    repeated_at          TEXT DEFAULT '',
    last_evaluated_at    TEXT DEFAULT '',
    last_delivery_status TEXT DEFAULT '',
    last_delivery_at     TEXT DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO alerts_next SELECT
    id, rule_id, service, state, value, fired_at, resolved_at, repeated_at,
    last_evaluated_at, last_delivery_status, last_delivery_at, created_at
FROM alerts;
DROP TABLE alerts;
ALTER TABLE alerts_next RENAME TO alerts;
CREATE UNIQUE INDEX alerts_open ON alerts (rule_id, service) WHERE state IN ('pending', 'firing');
CREATE INDEX alerts_rule_service ON alerts (rule_id, service, created_at);
