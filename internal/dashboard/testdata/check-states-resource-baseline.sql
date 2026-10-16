WITH raw AS (
 SELECT series_id,ingested_unix_nano,namespace,time,value,type,attributes['http.url']::VARCHAR AS url,
 attributes['http.method']::VARCHAR AS method,resource['host.name']::VARCHAR AS host,
 attributes['http.status_class']::VARCHAR AS class,attributes['http.status_code']::VARCHAR AS code
 FROM metrics WHERE time >= make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)::TIMESTAMPTZ_NS
 AND time < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS AND name='httpcheck.status' QUALIFY row_number() OVER (PARTITION BY series_id,time ORDER BY ingested_unix_nano DESC,value DESC)=1),
 keyed AS (SELECT *,to_json([namespace,url,method,host])::VARCHAR AS entity,
 coalesce(namespace,'')||' / '||coalesce(method,'method unknown')||' '||coalesce(url,'endpoint unknown')||' from '||coalesce(host,'vantage unknown') AS endpoint_from FROM raw),
 service_hosts AS (SELECT DISTINCT namespace,resource['host.name']::VARCHAR AS host FROM spans WHERE $__window(start_time) AND service IS NOT NULL AND service<>''),
 check_hosts AS (SELECT DISTINCT namespace,host FROM keyed WHERE $__window(time)),
 vantage AS (SELECT count(*)>0 AND bool_and(coalesce(host<>'',false) AND EXISTS (SELECT 1 FROM service_hosts s WHERE s.namespace=check_hosts.namespace AND s.host=check_hosts.host)) AS all_local FROM check_hosts),
 inventory AS (SELECT DISTINCT entity,endpoint_from||CASE WHEN all_local THEN ' (all checks run on service hosts; outside reachability unverified)' ELSE '' END AS endpoint_from FROM keyed CROSS JOIN vantage WHERE $__window(time)),
 attempts AS (SELECT entity,time,
 CASE WHEN bool_or(url IS NULL OR url='' OR method IS NULL OR method='' OR host IS NULL OR host='' OR type NOT IN ('gauge','updown') OR NOT isfinite(value) OR (class IS NULL AND code IS NULL)) THEN 2
 WHEN bool_or(value>0 AND (class IN ('2xx','3xx') OR try_cast(code AS INTEGER) BETWEEN 200 AND 399)) THEN 0 ELSE 1 END AS failure
 FROM keyed GROUP BY entity,time),
 gaps AS (SELECT *,epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(lag(time) OVER (PARTITION BY entity ORDER BY time)::TIMESTAMP_NS)::HUGEINT AS gap_ns FROM attempts),
 cadence AS (SELECT *,last_value(CASE WHEN gap_ns>0 THEN gap_ns END IGNORE NULLS) OVER (PARTITION BY entity ORDER BY time) AS interval_ns FROM gaps),
 buckets AS (SELECT b::TIMESTAMPTZ_NS AS time,least((b+INTERVAL '1 minute')::TIMESTAMPTZ_NS,$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS) AS at
 FROM generate_series(time_bucket(INTERVAL '1 minute',$__from::TIMESTAMP_NS),$__to::TIMESTAMP_NS,INTERVAL '1 minute') t(b) WHERE b::TIMESTAMPTZ_NS < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 grid AS (SELECT * FROM inventory CROSS JOIN buckets),
 states AS (SELECT g.time,g.endpoint_from,g.entity,
 CASE WHEN a.interval_ns IS NULL OR a.failure=2 THEN 2 WHEN epoch_ns(g.at::TIMESTAMP_NS)::HUGEINT-epoch_ns(a.time::TIMESTAMP_NS)::HUGEINT>=2*a.interval_ns THEN 2 ELSE a.failure END::DOUBLE AS failure,
 CASE WHEN a.interval_ns IS NULL THEN 'reporting interval unknown' WHEN epoch_ns(g.at::TIMESTAMP_NS)::HUGEINT-epoch_ns(a.time::TIMESTAMP_NS)::HUGEINT>=2*a.interval_ns THEN 'no recent result' WHEN a.failure=2 THEN 'check metadata unsupported or missing' WHEN a.failure=1 THEN 'down' ELSE 'reachable' END AS state
 FROM grid g ASOF LEFT JOIN cadence a ON g.entity=a.entity AND g.at>a.time),
 ranked AS (SELECT *,arg_max(failure,time) OVER (PARTITION BY entity) AS current_failure FROM states)
 SELECT time,endpoint_from,failure,state FROM ranked ORDER BY current_failure DESC,time DESC,endpoint_from
