-- Remove user-managed API keys. Remote MCP clients now use OAuth 2.1.
DROP INDEX users_key;
ALTER TABLE users DROP COLUMN key;

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
