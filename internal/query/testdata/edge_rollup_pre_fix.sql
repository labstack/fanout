
WITH affected AS (
  SELECT DISTINCT namespace, date_trunc('minute', start_time) AS bucket
  FROM spans
  WHERE ingested_unix_nano > ?
    AND ingested_unix_nano <= ?
    AND start_time IS NOT NULL
    AND start_time >= ?
    AND start_time < ?
),
call_edges AS (
  SELECT
    child.namespace,
    date_trunc('minute', child.start_time) AS bucket,
    parent.service AS caller,
    child.service AS callee,
    COUNT(*) AS calls,
    AVG(child.duration_ms) AS avg_ms,
    AVG(CASE WHEN child.status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) AS error_rate,
    'call' AS edge_type
  FROM spans child
  JOIN spans parent
    ON child.parent_span_id = parent.span_id
   AND child.trace_id = parent.trace_id
   AND child.namespace = parent.namespace
   -- Bound the parent side to the affected BUCKET RANGE ±1h: without this
   -- the hash build covers every span ever ingested. Parents more than 1h
   -- outside [MIN(affected.bucket), MAX(affected.bucket)] are dropped —
   -- acceptable for minute-bucket dependency edges. Caveat: buckets come
   -- from span start_time while the window bounds ingested_unix_nano, so
   -- one late-arriving span with an old start_time widens the range (and
   -- this scan) back to that bucket for the pass that ingests it.
   AND parent.start_time >= (SELECT MIN(bucket) FROM affected) - INTERVAL 1 HOUR
   AND parent.start_time <= (SELECT MAX(bucket) FROM affected) + INTERVAL 1 HOUR
  JOIN affected a
    ON a.namespace = child.namespace
   AND a.bucket = date_trunc('minute', child.start_time)
  WHERE parent.service IS NOT NULL
    AND parent.service != ''
    AND child.service IS NOT NULL
    AND child.service != ''
    AND parent.service != child.service
    AND child.start_time >= (SELECT MIN(bucket) FROM affected)
    AND child.start_time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
  GROUP BY child.namespace, date_trunc('minute', child.start_time), parent.service, child.service
),
-- Producers and consumers are aggregated per (namespace, bucket, service,
-- destination, msg_system) BEFORE the join — joining raw span rows multiplies
-- producer spans by consumer spans per destination, quadratic in per-bucket
-- span count. calls = consumed messages on the destination, attributed to
-- each producer service publishing to it (a message consumed once appears on
-- every producer's edge).
producers AS (
  SELECT DISTINCT
    s.namespace,
    date_trunc('minute', s.start_time) AS bucket,
    s.service,
    json_extract_string(s.attributes_json, '$."messaging.destination.name"') AS destination,
    json_extract_string(s.attributes_json, '$."messaging.system"') AS msg_system
  FROM spans s
  JOIN affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time)
  WHERE s.kind = 'SPAN_KIND_PRODUCER'
    AND s.start_time >= (SELECT MIN(bucket) FROM affected)
    AND s.start_time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
    AND s.service IS NOT NULL
    AND s.service != ''
    AND json_extract_string(s.attributes_json, '$."messaging.destination.name"') IS NOT NULL
),
consumers AS (
  SELECT
    s.namespace,
    date_trunc('minute', s.start_time) AS bucket,
    s.service,
    json_extract_string(s.attributes_json, '$."messaging.destination.name"') AS destination,
    json_extract_string(s.attributes_json, '$."messaging.system"') AS msg_system,
    COUNT(*) AS calls
  FROM spans s
  JOIN affected a
    ON a.namespace = s.namespace
   AND a.bucket = date_trunc('minute', s.start_time)
  WHERE s.kind = 'SPAN_KIND_CONSUMER'
    AND s.start_time >= (SELECT MIN(bucket) FROM affected)
    AND s.start_time < (SELECT MAX(bucket) FROM affected) + INTERVAL 1 MINUTE
    AND s.service IS NOT NULL
    AND s.service != ''
    AND json_extract_string(s.attributes_json, '$."messaging.destination.name"') IS NOT NULL
  GROUP BY s.namespace, date_trunc('minute', s.start_time), s.service,
    json_extract_string(s.attributes_json, '$."messaging.destination.name"'),
    json_extract_string(s.attributes_json, '$."messaging.system"')
),
messaging_edges AS (
  SELECT
    p.namespace,
    p.bucket,
    p.service AS caller,
    c.service AS callee,
    SUM(c.calls) AS calls,
    0.0 AS avg_ms,
    0.0 AS error_rate,
    'messaging' AS edge_type
  FROM producers p
  JOIN consumers c
    ON c.namespace = p.namespace
   AND c.bucket = p.bucket
   AND c.destination = p.destination
   AND c.msg_system = p.msg_system
  WHERE p.service != c.service
  GROUP BY p.namespace, p.bucket, p.service, c.service
)
INSERT INTO edge_rollup (
  namespace,
  bucket,
  caller,
  callee,
  calls,
  avg_ms,
  error_rate,
  edge_type
)
SELECT namespace, bucket, caller, callee, calls, avg_ms, error_rate, edge_type
FROM call_edges
UNION ALL
SELECT namespace, bucket, caller, callee, calls, avg_ms, error_rate, edge_type
FROM messaging_edges;