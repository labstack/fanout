ALTER TABLE `agui_threads` ADD COLUMN `title_derived` text NOT NULL DEFAULT 'Untitled investigation';

UPDATE `agui_threads`
SET `title_derived` = COALESCE(
  (
    SELECT
      CASE
        WHEN length(trim(replace(replace(replace(json_extract(value, '$.content'), char(10), ' '), char(13), ' '), char(9), ' '))) <= 72
          THEN trim(replace(replace(replace(json_extract(value, '$.content'), char(10), ' '), char(13), ' '), char(9), ' '))
        ELSE rtrim(substr(trim(replace(replace(replace(json_extract(value, '$.content'), char(10), ' '), char(13), ' '), char(9), ' ')), 1, 72)) || '…'
      END
    FROM json_each(
      CASE
        WHEN json_valid(`agui_threads`.`messages_json`) THEN `agui_threads`.`messages_json`
        ELSE '[]'
      END
    )
    WHERE json_extract(value, '$.role') = 'user'
      AND json_type(value, '$.content') = 'text'
      AND trim(json_extract(value, '$.content')) <> ''
    ORDER BY CAST(key AS INTEGER)
    LIMIT 1
  ),
  'Untitled investigation'
);
