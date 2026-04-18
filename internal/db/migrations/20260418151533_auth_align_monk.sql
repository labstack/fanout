-- Disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- Create "new_users" table
CREATE TABLE `new_users` (
  `id` text NULL,
  `email` text NOT NULL,
  `name` text NULL DEFAULT '',
  `role` text NOT NULL DEFAULT 'operator',
  `active` integer NOT NULL DEFAULT 1,
  `key` text NULL,
  `logged_in_at` text NULL DEFAULT '',
  `created_at` text NOT NULL DEFAULT (datetime('now')),
  `updated_at` text NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (`id`)
);
-- Copy rows from old table "users" to new temporary table "new_users"
INSERT INTO `new_users` (`id`, `email`, `name`, `role`, `active`, `logged_in_at`, `created_at`, `updated_at`) SELECT `id`, `email`, `name`, `role`, `active`, `logged_in_at`, `created_at`, `updated_at` FROM `users`;
-- Drop "users" table after copying rows
DROP TABLE `users`;
-- Rename temporary table "new_users" to "users"
ALTER TABLE `new_users` RENAME TO `users`;
-- Create index "users_email" to table: "users"
CREATE UNIQUE INDEX `users_email` ON `users` (`email`);
-- Create index "users_key" to table: "users"
CREATE UNIQUE INDEX `users_key` ON `users` (`key`);
-- Drop "verification_codes" table
DROP TABLE `verification_codes`;
-- Create "verifications" table
CREATE TABLE `verifications` (
  `id` text NULL,
  `email` text NOT NULL,
  `code_hash` text NOT NULL,
  `attempts` integer NOT NULL DEFAULT 0,
  `used` integer NOT NULL DEFAULT 0,
  `expires_at` text NOT NULL,
  `created_at` text NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (`id`)
);
-- Create index "idx_verifications_email" to table: "verifications"
CREATE INDEX `idx_verifications_email` ON `verifications` (`email`);
-- Enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;
