-- Fanout Schema (SQLite)
-- Source of truth for Atlas migrations and sqlc generation.
-- Only covers application state in SQLite — DuckDB has its own schema.

CREATE TABLE alert_rules (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    description       TEXT DEFAULT '',
    enabled           INTEGER NOT NULL DEFAULT 1,
    service           TEXT DEFAULT '',
    namespace         TEXT DEFAULT '',
    expression        TEXT NOT NULL,
    for_seconds       INTEGER NOT NULL DEFAULT 60,
    cooldown_s        INTEGER NOT NULL DEFAULT 600,
    repeat_interval_s INTEGER NOT NULL DEFAULT 3600,
    webhook_url       TEXT DEFAULT '',
    webhook_headers   TEXT DEFAULT '',
    webhook_template  TEXT DEFAULT '',
    notify_on_resolve INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE alerts (
    id                   TEXT PRIMARY KEY,
    rule_id              TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    service              TEXT NOT NULL,
    state                TEXT NOT NULL,
    value                REAL DEFAULT 0,
    fired_at             TEXT DEFAULT '',
    resolved_at          TEXT DEFAULT '',
    repeated_at          TEXT DEFAULT '',
    last_eval            TEXT DEFAULT '',
    last_delivery_status TEXT DEFAULT '',
    last_delivery_at     TEXT DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(rule_id, service)
);

CREATE TABLE users (
    id           TEXT PRIMARY KEY,
    email        TEXT NOT NULL UNIQUE,
    name         TEXT DEFAULT '',
    role         TEXT NOT NULL DEFAULT 'operator',
    active       INTEGER NOT NULL DEFAULT 1,
    key          TEXT UNIQUE,
    logged_in_at TEXT DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE verifications (
    id         TEXT PRIMARY KEY,
    email      TEXT NOT NULL,
    code_hash  TEXT NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    used       INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_verifications_email ON verifications(email);

CREATE TABLE config (
    group_key   TEXT PRIMARY KEY,
    overrides   TEXT NOT NULL DEFAULT '{}',
    updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_by  TEXT DEFAULT '',
    last_reason TEXT DEFAULT ''
);
