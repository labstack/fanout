-- Create "settings" table
CREATE TABLE `settings` (
  `key` text NOT NULL,
  `value` text NOT NULL DEFAULT '{}',
  `updated_at` text NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (`key`)
);
-- Preserve ingest row from old "config" table; drop bootstrap (regenerated in memory).
INSERT INTO `settings` (`key`, `value`, `updated_at`)
SELECT `group_key`, `overrides`, `updated_at`
FROM `config`
WHERE `group_key` != 'bootstrap';
-- Drop legacy "config" table
DROP TABLE `config`;
