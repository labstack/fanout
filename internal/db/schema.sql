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

CREATE TABLE oauth_clients (
    client_id                  TEXT PRIMARY KEY,
    client_name                TEXT NOT NULL,
    client_uri                 TEXT NOT NULL DEFAULT '',
    redirect_uris_json         TEXT NOT NULL,
    grant_types_json           TEXT NOT NULL,
    response_types_json        TEXT NOT NULL,
    token_endpoint_auth_method TEXT NOT NULL,
    created_at                 INTEGER NOT NULL
);

CREATE TABLE oauth_authorization_codes (
    code_hash      TEXT PRIMARY KEY,
    client_id      TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri   TEXT NOT NULL,
    scope          TEXT NOT NULL,
    resource       TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    expires_at     INTEGER NOT NULL,
    created_at     INTEGER NOT NULL
);

CREATE INDEX oauth_authorization_codes_expires
    ON oauth_authorization_codes(expires_at);

CREATE TABLE oauth_tokens (
    token_hash TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    family_id  TEXT NOT NULL,
    client_id  TEXT NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope      TEXT NOT NULL,
    resource   TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    revoked_at INTEGER,
    created_at INTEGER NOT NULL
);

CREATE INDEX oauth_tokens_family ON oauth_tokens(family_id);
CREATE INDEX oauth_tokens_expires ON oauth_tokens(expires_at);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE agui_threads (
    thread_id     TEXT PRIMARY KEY,
    owner_id      TEXT NOT NULL,
    messages_json TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_agui_threads_owner_updated
    ON agui_threads(owner_id, updated_at DESC);

CREATE TABLE agui_runs (
    run_id        TEXT PRIMARY KEY,
    thread_id     TEXT NOT NULL REFERENCES agui_threads(thread_id) ON DELETE CASCADE,
    parent_run_id TEXT,
    input_json    TEXT NOT NULL,
    events_json   TEXT NOT NULL DEFAULT '[]',
    status        TEXT NOT NULL,
    error         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at  TEXT
);

CREATE INDEX idx_agui_runs_thread_created
    ON agui_runs(thread_id, created_at DESC);
