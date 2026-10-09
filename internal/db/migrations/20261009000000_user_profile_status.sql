-- +goose Up
ALTER TABLE users RENAME COLUMN name TO display_name;
ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended'));
UPDATE users SET status = CASE WHEN active = 1 THEN 'active' ELSE 'suspended' END;
ALTER TABLE users DROP COLUMN active;
