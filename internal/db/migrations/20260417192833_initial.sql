-- Create "alert_rules" table
CREATE TABLE `alert_rules` (
  `id` text NOT NULL,
  `name` text NOT NULL,
  `description` text NULL DEFAULT '',
  `enabled` integer NOT NULL DEFAULT 1,
  `service` text NULL DEFAULT '',
  `namespace` text NULL DEFAULT '',
  `expression` text NOT NULL,
  `for_seconds` integer NOT NULL DEFAULT 60,
  `cooldown_s` integer NOT NULL DEFAULT 600,
  `repeat_interval_s` integer NOT NULL DEFAULT 3600,
  `webhook_url` text NULL DEFAULT '',
  `webhook_headers` text NULL DEFAULT '',
  `webhook_template` text NULL DEFAULT '',
  `notify_on_resolve` integer NOT NULL DEFAULT 0,
  `created_at` text NOT NULL DEFAULT (datetime('now')),
  `updated_at` text NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (`id`)
);
-- Create "alerts" table
CREATE TABLE `alerts` (
  `id` text NOT NULL,
  `rule_id` text NOT NULL,
  `service` text NOT NULL,
  `state` text NOT NULL,
  `value` real NULL DEFAULT 0,
  `fired_at` text NULL DEFAULT '',
  `resolved_at` text NULL DEFAULT '',
  `repeated_at` text NULL DEFAULT '',
  `last_eval` text NULL DEFAULT '',
  `last_delivery_status` text NULL DEFAULT '',
  `last_delivery_at` text NULL DEFAULT '',
  `created_at` text NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (`id`),
  CONSTRAINT `0` FOREIGN KEY (`rule_id`) REFERENCES `alert_rules` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "alerts_rule_id_service" to table: "alerts"
CREATE UNIQUE INDEX `alerts_rule_id_service` ON `alerts` (`rule_id`, `service`);
-- Create "users" table
CREATE TABLE `users` (
  `id` text NOT NULL,
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
-- Create index "users_email" to table: "users"
CREATE UNIQUE INDEX `users_email` ON `users` (`email`);
-- Create index "users_key" to table: "users"
CREATE UNIQUE INDEX `users_key` ON `users` (`key`);
-- Create "verifications" table
CREATE TABLE `verifications` (
  `id` text NOT NULL,
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
-- Create "config" table
CREATE TABLE `config` (
  `group_key` text NOT NULL,
  `overrides` text NOT NULL DEFAULT '{}',
  `updated_at` text NOT NULL DEFAULT (datetime('now')),
  `updated_by` text NULL DEFAULT '',
  `last_reason` text NULL DEFAULT '',
  PRIMARY KEY (`group_key`)
);
