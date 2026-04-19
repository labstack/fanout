DROP TABLE `config`;
CREATE TABLE `settings` (
  `key` text NOT NULL,
  `value` text NOT NULL,
  `updated_at` text NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (`key`)
);
