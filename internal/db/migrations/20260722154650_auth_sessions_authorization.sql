ALTER TABLE users ADD COLUMN auth_version INTEGER NOT NULL DEFAULT 1;

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

CREATE INDEX user_identities_user_idx ON user_identities(user_id);

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
