-- name: ListDashboards :many
SELECT d.id, d.name, d.description, d.is_default, d.version, d.panel_count, d.updated_at,
       o.thread_id AS origin_thread_id, o.message_id AS origin_message_id, o.request_excerpt AS origin_request_excerpt
FROM dashboards d
LEFT JOIN dashboard_origins o ON o.dashboard_id = d.id
WHERE d.owner_id = ?
ORDER BY d.is_default DESC, d.updated_at DESC;

-- name: InsertDashboardOrigin :execrows
INSERT INTO dashboard_origins (dashboard_id, thread_id, message_id, request_excerpt)
SELECT sqlc.arg(dashboard_id), sqlc.arg(thread_id), sqlc.arg(message_id), sqlc.arg(request_excerpt)
WHERE EXISTS (SELECT 1 FROM agui_threads t WHERE t.thread_id = sqlc.arg(thread_id) AND t.owner_id = sqlc.arg(owner_id))
  AND EXISTS (SELECT 1 FROM dashboards d WHERE d.id = sqlc.arg(dashboard_id) AND d.owner_id = sqlc.arg(owner_id));

-- name: CountDashboards :one
SELECT COUNT(*) FROM dashboards WHERE owner_id = ?;

-- name: GetDashboard :one
SELECT * FROM dashboards WHERE id = ? AND owner_id = ?;

-- name: InsertDashboardBelowOwnerLimit :execrows
INSERT INTO dashboards (id, owner_id, name, description, is_default, version, spec_json, panel_count, created_at, updated_at)
SELECT
    CAST(sqlc.arg(id) AS TEXT), CAST(sqlc.arg(owner_id) AS TEXT), CAST(sqlc.arg(name) AS TEXT), CAST(sqlc.arg(description) AS TEXT),
    NOT EXISTS (SELECT 1 FROM dashboards WHERE owner_id = CAST(sqlc.arg(owner_id) AS TEXT)),
    CAST(sqlc.arg(version) AS INTEGER), CAST(sqlc.arg(spec_json) AS TEXT), CAST(sqlc.arg(panel_count) AS INTEGER), CAST(sqlc.arg(created_at) AS TEXT), CAST(sqlc.arg(updated_at) AS TEXT)
WHERE (SELECT COUNT(*) FROM dashboards WHERE owner_id = CAST(sqlc.arg(owner_id) AS TEXT)) < 500;

-- name: UpdateDashboard :execrows
UPDATE dashboards
SET name = sqlc.arg(name),
    description = sqlc.arg(description),
    version = sqlc.arg(next_version),
    spec_json = sqlc.arg(spec_json),
    panel_count = sqlc.arg(panel_count),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND owner_id = sqlc.arg(owner_id) AND version = sqlc.arg(base_version);

-- name: DeleteDashboard :execrows
DELETE FROM dashboards WHERE id = ? AND owner_id = ?;

-- name: PromoteNewestDashboard :exec
UPDATE dashboards SET is_default = 1
WHERE id = (SELECT d.id FROM dashboards d WHERE d.owner_id = ? ORDER BY d.updated_at DESC LIMIT 1);

-- name: InsertDashboardVersion :exec
INSERT INTO dashboard_versions (dashboard_id, version, spec_json, author_kind, author_id, message, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListDashboardVersions :many
SELECT version, author_kind, author_id, message, created_at
FROM dashboard_versions
WHERE dashboard_id = ?
ORDER BY version DESC
LIMIT 100;

-- name: GetDashboardVersion :one
SELECT spec_json FROM dashboard_versions WHERE dashboard_id = ? AND version = ?;

-- name: PruneDashboardVersions :exec
DELETE FROM dashboard_versions
WHERE dashboard_id = sqlc.arg(dashboard_id) AND version < sqlc.arg(keep_from);
