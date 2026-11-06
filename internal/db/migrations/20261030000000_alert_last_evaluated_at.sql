-- +goose Up
ALTER TABLE alerts RENAME COLUMN last_eval TO last_evaluated_at;
