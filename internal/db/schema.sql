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
    auth_version INTEGER NOT NULL DEFAULT 1,
    logged_in_at TEXT DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);


-- Older installations retain SQLite datetime defaults that cannot be altered in
-- place. Normalize any default-generated values after insert; application
-- writes already use the same fixed-width UTC format directly.
CREATE TRIGGER users_normalize_timestamps_after_insert
AFTER INSERT ON users
WHEN instr(NEW.created_at, ' ') > 0 OR instr(NEW.updated_at, ' ') > 0
BEGIN
  UPDATE users SET
    created_at = strftime('%Y-%m-%dT%H:%M:%fZ', NEW.created_at),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', NEW.updated_at)
  WHERE id = NEW.id;
END;

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

CREATE TABLE sessions (
    token_hash          TEXT PRIMARY KEY,
    user_id             TEXT REFERENCES users(id) ON DELETE CASCADE,
    data                BLOB NOT NULL,
    created_at          INTEGER NOT NULL,
    last_activity_at    INTEGER NOT NULL,
    absolute_expires_at INTEGER NOT NULL
);

CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_idle_idx ON sessions(last_activity_at);
CREATE INDEX sessions_expiry_idx ON sessions(absolute_expires_at);

CREATE TABLE user_identities (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    email_at_link TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_login_at TEXT,
    UNIQUE (issuer, subject)
);

CREATE UNIQUE INDEX user_identities_user_unique_idx ON user_identities(user_id);

CREATE TABLE auth_audit_events (
    id            TEXT PRIMARY KEY,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    event_type    TEXT NOT NULL,
    outcome       TEXT NOT NULL,
    target_type   TEXT,
    target_id     TEXT,
    remote_ip     TEXT,
    user_agent    TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at    TEXT NOT NULL
);

CREATE INDEX auth_audit_events_created_idx
    ON auth_audit_events(created_at DESC);
CREATE INDEX auth_audit_events_actor_idx
    ON auth_audit_events(actor_user_id, created_at DESC);

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
    title_override TEXT,
    title_derived TEXT NOT NULL DEFAULT 'Untitled investigation',
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

-- Legacy single-canvas state retained for one-way lazy migration.
CREATE TABLE dashboard_state (
    owner_id  TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    state_json TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE dashboards (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL COLLATE NOCASE,
    description TEXT NOT NULL DEFAULT '',
    window      TEXT NOT NULL DEFAULT '1h',
    namespace   TEXT NOT NULL DEFAULT '',
    is_default  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE UNIQUE INDEX dashboards_owner_name ON dashboards(owner_id, name);
CREATE INDEX dashboards_owner_updated ON dashboards(owner_id, updated_at DESC);
CREATE UNIQUE INDEX dashboards_owner_default ON dashboards(owner_id) WHERE is_default = 1;

CREATE TABLE dashboard_widgets (
    id           TEXT NOT NULL,
    dashboard_id TEXT NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    type         TEXT NOT NULL,
    title        TEXT NOT NULL,
    config_json  TEXT NOT NULL DEFAULT '{}',
    enabled      INTEGER NOT NULL DEFAULT 1,
    x            INTEGER NOT NULL,
    y            INTEGER NOT NULL,
    w            INTEGER NOT NULL,
    h            INTEGER NOT NULL,
    min_w        INTEGER NOT NULL DEFAULT 1,
    min_h        INTEGER NOT NULL DEFAULT 1,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (dashboard_id, id)
);

CREATE INDEX dashboard_widgets_dashboard_order ON dashboard_widgets(dashboard_id, sort_order);
