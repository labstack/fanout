package queryrows

// CompletedTraceQuery ranks scoped traces from completed batch candidates and
// the uncached/boundary tail. Arguments are start, end, namespace twice, service
// twice, and limit. Both top-N stages must use the same limit. Results are
// namespace, trace_id, and the selected scope's has_error flag.
const CompletedTraceQuery = `WITH tail AS (
 SELECT namespace,trace_id,min(start_unix_nano) AS min_start,max(end_unix_nano) AS max_end,
 max(CASE WHEN status = 'STATUS_CODE_ERROR' THEN 1 ELSE 0 END) AS has_error
 FROM trace_tail
 WHERE start_time>=$1::TIMESTAMP_NS::TIMESTAMPTZ_NS AND start_time<$2::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND ($3='' OR namespace=$4) AND ($5='' OR service=$6) AND trace_id<>'' GROUP BY namespace,trace_id
), untouched AS (
 SELECT namespace,trace_id,min_start,max_end,has_error FROM trace_candidates
 WHERE (namespace,trace_id) NOT IN (SELECT namespace,trace_id FROM tail)
 ORDER BY has_error DESC,max_end-min_start DESC,trace_id,namespace LIMIT $7
), touched AS (
 SELECT t.namespace,t.trace_id, least(t.min_start,c.min_start) AS min_start,greatest(t.max_end,c.max_end) AS max_end,
 greatest(t.has_error,c.has_error) AS has_error
 FROM tail t LEFT JOIN trace_candidates c ON t.namespace=c.namespace AND t.trace_id=c.trace_id
), candidates AS (SELECT * FROM untouched UNION ALL SELECT * FROM touched)
SELECT namespace,trace_id,has_error FROM candidates ORDER BY has_error DESC,max_end-min_start DESC,trace_id,namespace LIMIT $7`
