ALTER TABLE `verifications` ADD COLUMN `purpose` text NOT NULL DEFAULT 'email_code';

DROP INDEX `idx_verifications_email`;

CREATE INDEX `idx_verifications_email_purpose` ON `verifications` (`email`, `purpose`, `created_at` DESC);

CREATE INDEX `idx_verifications_purpose_hash` ON `verifications` (`purpose`, `code_hash`);
