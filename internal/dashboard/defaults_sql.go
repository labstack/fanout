package dashboard

import "strings"

// SQL for the saved default dashboard, executed through the ordinary panel guard.
// Merge grid cells with observations instead of ASOF: low cardinality estimates
// can choose a quadratic nested-loop join. Cells sort before observations at the
// same instant, preserving the strict observation.time < cell.end contract.
const reachabilitySQL = `WITH raw AS (
 SELECT namespace,host,entity,endpoint_from,p.time,p.flag FROM receiver_entity_rollup,
 unnest([first_observation,CASE WHEN previous_observation.time<>first_observation.time THEN previous_observation END,CASE WHEN last_observation.time<>first_observation.time THEN last_observation END]) t(p)
 WHERE receiver='check' AND family='httpcheck.status'
 AND bucket>=time_bucket(INTERVAL '1 minute',make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)) AND bucket<$__to::TIMESTAMP_NS
 AND p.time>=make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)::TIMESTAMPTZ_NS AND p.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 keyed AS (SELECT * FROM raw),
 check_hosts AS (SELECT DISTINCT namespace,host FROM keyed WHERE $__window(time)),
 service_hosts AS (SELECT DISTINCT s.namespace,s.host FROM (SELECT namespace,resource['host.name']::VARCHAR AS host FROM resource_rollup WHERE $__window(bucket) AND signal='spans' AND service IS NOT NULL AND service<>'') s
 SEMI JOIN (SELECT namespace,host FROM check_hosts WHERE host<>'') c ON s.namespace=c.namespace AND s.host=c.host),
 vantage AS (SELECT count(*)>0 AND bool_and(coalesce(host<>'',false) AND EXISTS (SELECT 1 FROM service_hosts s WHERE s.namespace=check_hosts.namespace AND s.host=check_hosts.host)) AS all_local FROM check_hosts),
 inventory AS (SELECT DISTINCT entity,endpoint_from||CASE WHEN all_local THEN ' (all checks run on service hosts; outside reachability unverified)' ELSE '' END AS endpoint_from FROM keyed CROSS JOIN vantage WHERE $__window(time)),
 attempts AS (SELECT entity,time,CASE max(flag) WHEN 2 THEN 2 WHEN 1 THEN 0 ELSE 1 END AS failure FROM keyed GROUP BY entity,time),
 gaps AS (SELECT *,epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(lag(time) OVER (PARTITION BY entity ORDER BY time)::TIMESTAMP_NS)::HUGEINT AS gap_ns FROM attempts),
 cadence AS (SELECT *,last_value(CASE WHEN gap_ns>0 THEN gap_ns END IGNORE NULLS) OVER (PARTITION BY entity ORDER BY time) AS interval_ns FROM gaps),
 buckets AS (SELECT b::TIMESTAMPTZ_NS AS time,least((b+INTERVAL '1 minute')::TIMESTAMPTZ_NS,$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS) AS at
 FROM generate_series(time_bucket(INTERVAL '1 minute',$__from::TIMESTAMP_NS),$__to::TIMESTAMP_NS,INTERVAL '1 minute') t(b) WHERE b::TIMESTAMPTZ_NS < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 grid AS (SELECT * FROM inventory CROSS JOIN buckets),
 events AS (SELECT entity,time AS event_at,1 AS row_kind,NULL::TIMESTAMPTZ_NS AS time,NULL::VARCHAR AS endpoint_from,
 struct_pack(time:=time,failure:=failure,interval_ns:=interval_ns) AS observation FROM cadence
 UNION ALL SELECT entity,g.at,0,time,endpoint_from,NULL FROM grid g),
 carried AS (SELECT *,last_value(observation IGNORE NULLS) OVER (PARTITION BY entity ORDER BY event_at,row_kind ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS latest FROM events),
 states AS (SELECT g.time,g.endpoint_from,g.entity,
 CASE WHEN g.latest.interval_ns IS NULL OR g.latest.failure=2 THEN 2 WHEN epoch_ns(g.event_at::TIMESTAMP_NS)::HUGEINT-epoch_ns(g.latest.time::TIMESTAMP_NS)::HUGEINT>=2*g.latest.interval_ns THEN 2 ELSE g.latest.failure END::DOUBLE AS failure,
 CASE WHEN g.latest.interval_ns IS NULL THEN 'reporting interval unknown' WHEN epoch_ns(g.event_at::TIMESTAMP_NS)::HUGEINT-epoch_ns(g.latest.time::TIMESTAMP_NS)::HUGEINT>=2*g.latest.interval_ns THEN 'no recent result' WHEN g.latest.failure=2 THEN 'check metadata unsupported or missing' WHEN g.latest.failure=1 THEN 'down' ELSE 'reachable' END AS state
 FROM carried g WHERE g.row_kind=0),
 ranked AS (SELECT *,arg_max(failure,time) OVER (PARTITION BY entity) AS current_failure FROM states)
 SELECT time,endpoint_from,failure,state FROM ranked ORDER BY current_failure DESC,time DESC,endpoint_from`

const checkLatencySQL = `WITH snapshots AS (
 SELECT series_id,namespace||' / '||coalesce(method,'method unknown')||' '||coalesce(url,'endpoint unknown')||' from '||coalesce(host,'vantage unknown') AS endpoint_from,unit,
 CASE WHEN last_finite_point.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS THEN last_finite_point
 WHEN previous_finite_point.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS THEN previous_finite_point
 WHEN first_finite_point.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS THEN first_finite_point END point
 FROM receiver_rollup WHERE name='httpcheck.duration' AND type IN ('gauge','updown')
 AND bucket>=time_bucket(INTERVAL '1 minute',make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-300000000000)) AND bucket<$__to::TIMESTAMP_NS),
 points AS (SELECT series_id,endpoint_from,point.time AS time,point.value*CASE unit WHEN 's' THEN 1000 WHEN 'ms' THEN 1 WHEN 'us' THEN .001 WHEN 'µs' THEN .001 WHEN 'ns' THEN .000001 END AS duration_ms
 FROM snapshots WHERE point.time>=make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS),
 inventory AS (SELECT DISTINCT series_id,endpoint_from FROM points WHERE $__window(time)),
 buckets AS (SELECT b::TIMESTAMPTZ_NS AS time,least((b+INTERVAL '1 minute')::TIMESTAMPTZ_NS,$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS) AS at
 FROM generate_series(time_bucket(INTERVAL '1 minute',$__from::TIMESTAMP_NS),$__to::TIMESTAMP_NS,INTERVAL '1 minute') t(b) WHERE b::TIMESTAMPTZ_NS < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 grid AS (SELECT * FROM inventory CROSS JOIN buckets),
 events AS (SELECT series_id,time AS event_at,1 AS row_kind,NULL::TIMESTAMPTZ_NS AS time,NULL::VARCHAR AS endpoint_from,
 struct_pack(time:=time,duration_ms:=duration_ms) AS observation FROM points
 UNION ALL SELECT series_id,g.at,0,time,endpoint_from,NULL FROM grid g),
 carried AS (SELECT *,last_value(observation IGNORE NULLS) OVER (PARTITION BY series_id ORDER BY event_at,row_kind ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS latest FROM events)
 SELECT time,endpoint_from,sum(CASE WHEN latest.time>=make_timestamp_ns(epoch_ns(event_at::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS THEN latest.duration_ms END)::DOUBLE AS latency
 FROM carried WHERE row_kind=0 GROUP BY time,endpoint_from ORDER BY time DESC,endpoint_from`

// Bounded, retry-deduplicated witnesses supply recent capacity and cadence.
func capacityCTE(containers bool) string {
	names := "'system.cpu.time','system.memory.usage','system.filesystem.usage'"
	identity := "host IS NOT NULL AND host<>''"
	receiver := "host"
	entity := "host"
	priority := "CASE name WHEN 'system.cpu.time' THEN 1 WHEN 'system.memory.usage' THEN 2 ELSE 3 END"
	if containers {
		receiver = "container"
		identity += " AND container_id IS NOT NULL AND container_id<>''"
		names = "'container.cpu.utilization','container.cpu.limit','container.memory.usage.total','container.memory.usage.limit'"
		entity = "sha256(to_json([host,container_id]))"
		priority = "CASE name WHEN 'container.cpu.utilization' THEN 1 WHEN 'container.memory.usage.total' THEN 2 WHEN 'container.cpu.limit' THEN 4 ELSE 3 END"
	}
	return `WITH minutes AS MATERIALIZED (SELECT namespace,bucket,family AS name,host,container_id,container_name,entity,
 first_observation,previous_observation,last_observation,` + strings.ReplaceAll(priority, "name", "family") + ` AS priority
 FROM receiver_entity_rollup WHERE receiver='` + receiver + `' AND family IN (` + names + `) AND ` + identity + `
 AND bucket>=time_bucket(INTERVAL '1 minute',make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)) AND bucket<$__to::TIMESTAMP_NS
 AND last_observation.time>=make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)::TIMESTAMPTZ_NS AND first_observation.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 inventory AS (SELECT namespace,entity,any_value(host) host,any_value(container_id) container_id,arg_max(container_name,last_observation.time) container,max(last_observation.time) observed
 FROM minutes WHERE $__window(bucket) GROUP BY namespace,entity),
 witness_family AS (SELECT namespace,entity,min(priority) AS priority FROM minutes GROUP BY namespace,entity),
 witness_minutes AS (SELECT k.* FROM minutes k JOIN witness_family f USING(namespace,entity,priority)
 QUALIFY dense_rank() OVER (PARTITION BY namespace,entity ORDER BY bucket DESC)<=2),
 witness AS (SELECT DISTINCT namespace,entity,t.time FROM witness_minutes,
 unnest([first_observation.time,previous_observation.time,last_observation.time]) t(time)
 WHERE t.time>=make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)::TIMESTAMPTZ_NS AND t.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 cadence AS (SELECT namespace,entity,max(time) AS last_seen,epoch_ns(max(time)::TIMESTAMP_NS)::HUGEINT-epoch_ns(arg_max(time,time,2)[2]::TIMESTAMP_NS)::HUGEINT AS interval_ns FROM witness GROUP BY namespace,entity),
 gauge_minutes AS (SELECT namespace,` + entity + ` AS entity,series_id,name,unit,scope_name,state,mountpoint,device,
 CASE WHEN last_finite_point.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS THEN last_finite_point
 WHEN previous_finite_point.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS THEN previous_finite_point
 WHEN first_finite_point.time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS THEN first_finite_point END point
 FROM receiver_rollup WHERE bucket>=time_bucket(INTERVAL '1 minute',make_timestamp_ns(epoch_ns($__to::TIMESTAMP_NS)-300000000000)) AND bucket<$__to::TIMESTAMP_NS
 AND name IN (` + names + `) AND ` + identity + ` AND type IN ('gauge','updown')),
 latest AS (SELECT namespace,entity,series_id,name,point.value AS value,unit,scope_name,point.time AS time,state,mountpoint,device
 FROM gauge_minutes WHERE point.time>=make_timestamp_ns(epoch_ns($__to::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS
 QUALIFY row_number() OVER (PARTITION BY series_id ORDER BY point.time DESC)=1)`
}

const reportingSQL = `CASE WHEN i.host IS NULL OR i.host='' /* identity */ THEN 'stable resource identity missing'

 WHEN c.interval_ns IS NULL THEN 'reporting interval unknown'
 WHEN epoch_ns($__to::TIMESTAMP_NS)::HUGEINT-epoch_ns(c.last_seen::TIMESTAMP_NS)::HUGEINT >= 2*c.interval_ns THEN 'not reporting' ELSE 'reporting' END`

func hostCapacitySQL() string {
	return capacityCTE(false) + `,
 cpu_minutes AS (SELECT namespace,host,series_id,state,temporality,
 first_finite_point.time AS first_time,first_finite_point.value AS first_value,first_finite_point.start_time AS first_start,
 last_finite_point.time AS last_time,counter_increase,counter_seconds,
 lag(last_finite_point.time) OVER w AS previous_time,lag(last_finite_point.value) OVER w AS previous_value,lag(last_finite_point.start_time) OVER w AS previous_start
 FROM receiver_rollup WHERE name='system.cpu.time' AND type='counter' AND temporality IN ('delta','cumulative') AND last_finite_point IS NOT NULL
 AND bucket>=time_bucket(INTERVAL '1 minute',make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000))
 AND bucket<$__to::TIMESTAMP_NS WINDOW w AS (PARTITION BY series_id ORDER BY bucket)),
 cpu_delta AS (
 SELECT namespace,host AS entity,series_id,state,last_time AS time,counter_increase AS increase,first_time AS elapsed_from,counter_seconds AS summary_seconds
 FROM cpu_minutes WHERE counter_seconds>0 AND isfinite(counter_increase)
 UNION ALL
 SELECT namespace,host AS entity,series_id,state,first_time AS time,
 CASE WHEN temporality='delta' OR first_value<previous_value OR (first_start IS NOT NULL AND previous_start IS NOT NULL AND first_start<>previous_start) THEN first_value ELSE first_value-previous_value END AS increase,
 CASE WHEN temporality='delta' AND first_start<first_time THEN first_start ELSE previous_time END AS elapsed_from,NULL::DOUBLE AS summary_seconds
 FROM cpu_minutes WHERE isfinite(first_value) AND (temporality='delta' OR isfinite(previous_value))),
 cpu_overlap AS (SELECT *, coalesce(summary_seconds,(epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(elapsed_from::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9) AS elapsed_s,
 greatest(0,(epoch_ns(least(time,$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS)::TIMESTAMP_NS)::HUGEINT-epoch_ns(greatest(elapsed_from,$__from::TIMESTAMP_NS::TIMESTAMPTZ_NS)::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9)
 * CASE WHEN summary_seconds IS NULL THEN 1 ELSE summary_seconds/nullif((epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(elapsed_from::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9,0) END AS overlap_s FROM cpu_delta WHERE elapsed_from<time),
 cpu_intervals AS (SELECT *,CASE WHEN elapsed_s<=600 THEN increase*overlap_s/elapsed_s WHEN $__window(time) THEN increase END AS rate_share,
 CASE WHEN elapsed_s<=600 THEN overlap_s WHEN $__window(time) THEN elapsed_s END AS covered_s FROM cpu_overlap),
 cpu_series AS (SELECT namespace,entity,series_id,any_value(state) AS state,
 CASE WHEN max(time)<make_timestamp_ns(epoch_ns($__to::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS THEN NULL
 ELSE coalesce(sum(rate_share) FILTER (WHERE covered_s>0)/nullif(sum(covered_s) FILTER (WHERE covered_s>0),0),arg_max(increase/elapsed_s,time)) END AS cpu_rate FROM cpu_intervals GROUP BY namespace,entity,series_id),
 cpu AS (SELECT namespace,entity,100.0*sum(cpu_rate) FILTER (WHERE state<>'idle')/nullif(sum(cpu_rate),0) AS cpu_percent FROM cpu_series GROUP BY namespace,entity),
 memory AS (SELECT namespace,entity,100.0*sum(value) FILTER (WHERE state='used')/nullif(sum(value),0) AS memory_percent FROM latest WHERE name='system.memory.usage' GROUP BY namespace,entity),
 mounts AS (SELECT namespace,entity,mountpoint,device,sum(value) FILTER (WHERE state IN ('used','reserved')) AS used_bytes,sum(value) AS total_bytes FROM latest WHERE name='system.filesystem.usage' GROUP BY namespace,entity,mountpoint,device),
 fullest AS (SELECT *,100.0*used_bytes/nullif(total_bytes,0) AS filesystem_percent FROM mounts QUALIFY row_number() OVER (PARTITION BY namespace,entity ORDER BY filesystem_percent DESC NULLS LAST,mountpoint,device)=1),
 results AS (SELECT i.namespace,i.host,` + strings.ReplaceAll(reportingSQL, "/* identity */", "") + ` AS reporting,cpu.cpu_percent,m.memory_percent,f.filesystem_percent,f.mountpoint,f.used_bytes,f.total_bytes,coalesce(c.last_seen,i.observed) AS last_seen FROM inventory i LEFT JOIN cadence c USING(namespace,entity) LEFT JOIN cpu USING(namespace,entity) LEFT JOIN memory m USING(namespace,entity) LEFT JOIN fullest f USING(namespace,entity))
 SELECT namespace,host,CASE WHEN reporting<>'reporting' THEN 100 ELSE greatest(cpu_percent,memory_percent,filesystem_percent) END::DOUBLE AS status,reporting,cpu_percent,memory_percent,filesystem_percent,mountpoint,used_bytes,total_bytes,last_seen,
 CASE WHEN memory_percent IS NULL OR filesystem_percent IS NULL OR cpu_percent IS NULL THEN 'measurement missing, unsupported, stale or denominator unavailable' END AS measurement
 FROM results ORDER BY (reporting<>'reporting') DESC,status DESC NULLS LAST,namespace,host`
}
func containerCapacitySQL() string {
	// docker_stats reports percent of one core despite declaring unit "1".
	// Only a reported CPU limit makes that usage a capacity signal.
	return capacityCTE(true) + `, values_by_container AS (SELECT namespace,entity,
 sum(value*CASE WHEN scope_name='github.com/open-telemetry/opentelemetry-collector-contrib/receiver/dockerstatsreceiver' THEN 1 ELSE CASE unit WHEN '%' THEN 1 WHEN '1' THEN 100 END END) FILTER (WHERE name='container.cpu.utilization') AS cpu_percent,
 sum(value) FILTER (WHERE name='container.cpu.limit' AND scope_name='github.com/open-telemetry/opentelemetry-collector-contrib/receiver/dockerstatsreceiver' AND value>0) AS cpu_limit,
 sum(value) FILTER (WHERE name='container.memory.usage.total') AS total_bytes,sum(value) FILTER (WHERE name='container.memory.usage.limit') AS limit_bytes FROM latest GROUP BY namespace,entity),
 results AS (SELECT i.namespace,i.host,coalesce(i.container,i.container_id) AS container,` + strings.ReplaceAll(reportingSQL, "/* identity */", "OR i.container_id IS NULL OR i.container_id=''") + ` AS reporting,v.cpu_percent,v.cpu_limit,100.0*v.total_bytes/nullif(v.limit_bytes,0) AS memory_percent,v.total_bytes,v.limit_bytes,coalesce(c.last_seen,i.observed) AS last_seen FROM inventory i LEFT JOIN cadence c USING(namespace,entity) LEFT JOIN values_by_container v USING(namespace,entity))
 SELECT namespace,host,container,CASE WHEN reporting<>'reporting' THEN 100 ELSE greatest(cpu_percent/nullif(cpu_limit,0),memory_percent) END::DOUBLE AS status,reporting,cpu_percent,memory_percent,total_bytes,limit_bytes,last_seen,
 nullif(concat_ws('; ',CASE WHEN cpu_limit IS NULL THEN 'cpu limit not reported' END,
 CASE WHEN limit_bytes IS NULL OR limit_bytes=0 THEN 'memory limit not reported' WHEN cpu_percent IS NULL OR memory_percent IS NULL THEN 'measurement missing, unsupported or stale' END),'') AS measurement
 FROM results ORDER BY (reporting<>'reporting') DESC,status DESC NULLS LAST,namespace,host,container`
}

// Selected resource tuples come from analytical minutes. No partial-second
// Parquet scan is needed; resource_inventory retains its separate exact contract.
const versionObservationsSQL = `SELECT namespace,service,resource['service.version']::VARCHAR AS version,resource['process.runtime.name']::VARCHAR AS runtime_name,resource['process.runtime.version']::VARCHAR AS runtime_version,resource['telemetry.sdk.language']::VARCHAR AS sdk_language,resource['telemetry.sdk.version']::VARCHAR AS sdk_version,first_seen AS seen FROM resource_rollup WHERE $__window(bucket) AND $__window(first_seen) AND resource['service.version']::VARCHAR<>''`
