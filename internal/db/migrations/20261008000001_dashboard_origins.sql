-- +goose Up
CREATE TABLE dashboard_origins (
    dashboard_id TEXT PRIMARY KEY REFERENCES dashboards(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL REFERENCES agui_threads(thread_id) ON DELETE CASCADE,
    message_id TEXT NOT NULL,
    request_excerpt TEXT NOT NULL
);
CREATE INDEX dashboard_origins_thread_id ON dashboard_origins (thread_id);
