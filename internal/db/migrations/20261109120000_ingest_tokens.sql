-- +goose Up
CREATE TABLE ingest_tokens (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    hint         TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    created_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
    last_used_at TEXT,
    revoked_at   TEXT,
    revoked_by   TEXT REFERENCES users(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX idx_ingest_tokens_active_name
    ON ingest_tokens(name COLLATE NOCASE) WHERE revoked_at IS NULL;

INSERT INTO ingest_tokens (id, name, token_hash, hint, source, created_at)
SELECT lower(hex(randomblob(16))), 'default', json_extract(value, '$.token_hash'), '', 'migrated', updated_at
FROM settings
WHERE key = 'ingest'
  AND json_valid(value)
  AND length(json_extract(value, '$.token_hash')) = 64;

DELETE FROM settings WHERE key = 'ingest';
