-- Create named, owner-scoped dashboards. Widget placement is normalized so
-- individual views can be added or rearranged without rewriting an opaque blob.
CREATE TABLE dashboards (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL COLLATE NOCASE,
  description TEXT NOT NULL DEFAULT '',
  window TEXT NOT NULL DEFAULT '1h',
  namespace TEXT NOT NULL DEFAULT '',
  is_default INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX dashboards_owner_name ON dashboards(owner_id, name);
CREATE INDEX dashboards_owner_updated ON dashboards(owner_id, updated_at DESC);
CREATE UNIQUE INDEX dashboards_owner_default ON dashboards(owner_id) WHERE is_default = 1;

CREATE TABLE dashboard_widgets (
  id TEXT NOT NULL,
  dashboard_id TEXT NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  config_json TEXT NOT NULL DEFAULT '{}',
  enabled INTEGER NOT NULL DEFAULT 1,
  x INTEGER NOT NULL,
  y INTEGER NOT NULL,
  w INTEGER NOT NULL,
  h INTEGER NOT NULL,
  min_w INTEGER NOT NULL DEFAULT 1,
  min_h INTEGER NOT NULL DEFAULT 1,
  sort_order INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (dashboard_id, id)
);

CREATE INDEX dashboard_widgets_dashboard_order ON dashboard_widgets(dashboard_id, sort_order);
