CREATE TABLE `agui_threads` (
  `thread_id` text NOT NULL,
  `owner_id` text NOT NULL,
  `messages_json` text NOT NULL DEFAULT '[]',
  `created_at` text NOT NULL DEFAULT (datetime('now')),
  `updated_at` text NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (`thread_id`)
);
CREATE INDEX `idx_agui_threads_owner_updated` ON `agui_threads` (`owner_id`, `updated_at` DESC);
CREATE TABLE `agui_runs` (
  `run_id` text NOT NULL,
  `thread_id` text NOT NULL,
  `parent_run_id` text NULL,
  `input_json` text NOT NULL,
  `events_json` text NOT NULL DEFAULT '[]',
  `status` text NOT NULL,
  `error` text NOT NULL DEFAULT '',
  `created_at` text NOT NULL DEFAULT (datetime('now')),
  `completed_at` text NULL,
  PRIMARY KEY (`run_id`),
  CONSTRAINT `agui_runs_thread_id_fkey` FOREIGN KEY (`thread_id`) REFERENCES `agui_threads` (`thread_id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
CREATE INDEX `idx_agui_runs_thread_created` ON `agui_runs` (`thread_id`, `created_at` DESC);
