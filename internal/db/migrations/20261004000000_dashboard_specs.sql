-- +goose Up
-- Dashboards become typed v1 specs with a version history. The widget model
-- is replaced outright: Fanout is pre-release, so existing dashboards are
-- dropped and each owner receives the new default dashboard on next visit.
DROP TABLE dashboard_widgets;
DROP TABLE dashboards;

CREATE TABLE dashboards (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL COLLATE NOCASE,
    description TEXT NOT NULL DEFAULT '',
    is_default  INTEGER NOT NULL DEFAULT 0,
    version     INTEGER NOT NULL,
    spec_json   TEXT NOT NULL,
    panel_count INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE UNIQUE INDEX dashboards_owner_name ON dashboards(owner_id, name);
CREATE INDEX dashboards_owner_updated ON dashboards(owner_id, updated_at DESC);
CREATE UNIQUE INDEX dashboards_owner_default ON dashboards(owner_id) WHERE is_default = 1;

CREATE TABLE dashboard_versions (
    dashboard_id TEXT NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    version      INTEGER NOT NULL,
    spec_json    TEXT NOT NULL,
    author_kind  TEXT NOT NULL CHECK (author_kind IN ('user', 'agent', 'system')),
    author_id    TEXT NOT NULL DEFAULT '',
    message      TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    PRIMARY KEY (dashboard_id, version)
);
