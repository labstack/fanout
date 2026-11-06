-- +goose Up
-- Episode starts were SQLite's datetime('now') text or RFC 3339 with a
-- variable fraction, which do not sort as text. Rewrite them as RFC 3339 UTC
-- with nine fraction digits, the form the store writes.
UPDATE alerts SET created_at =
    strftime('%Y-%m-%dT%H:%M:%S', created_at) || '.' ||
    substr(CASE WHEN instr(created_at, '.') > 0 THEN rtrim(substr(created_at, instr(created_at, '.') + 1), 'Z') ELSE '' END || '000000000', 1, 9) || 'Z'
WHERE strftime('%Y-%m-%dT%H:%M:%S', created_at) IS NOT NULL;
