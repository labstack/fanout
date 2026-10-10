package panel

import "strings"

// These are ordinary guarded SQL panels; the only interpolated data is the
// quoted namespace. Metric values and resource identities remain query data.
func systemReachabilityPanels(namespace string) []Panel {
	return []Panel{
		{ID: "check_states", Title: "Is it reachable?", Viz: "state_timeline", Width: 12, Height: "m", SQL: systemScopedSQL(systemCheckSQL, namespace), Thresholds: []Threshold{{Value: 1, Status: "bad"}}, Better: "lower"},
		{ID: "check_latency", Title: "Check latency", Viz: "timeseries", Width: 12, Height: "m", SQL: systemScopedSQL(systemDurationSQL, namespace), Unit: "ms"},
	}
}
func systemLatencyPanel(namespace string, metrics []MetricSchema) Panel {
	hasScalar, hasHistogram, unsupported := false, false, false
	for _, m := range metrics {
		if m.Name == "httpcheck.duration" {
			supported := systemDurationFactor(m.Unit) != ""
			hasScalar = hasScalar || supported && (m.Type == "gauge" || m.Type == "updown")
			hasHistogram = hasHistogram || supported && m.Type == "histogram"
			unsupported = unsupported || !supported || (m.Type != "gauge" && m.Type != "updown" && m.Type != "histogram")
		}
	}
	if hasHistogram && !hasScalar && !unsupported {
		return Panel{ID: "check_latency", Title: "Check latency (p95)", Viz: "timeseries", Width: 12, Height: "m", Unit: "ms", SQL: systemScopedSQL(systemDurationHistogramSQL+", quantiles AS ("+histogramQuantileSQL("weights_input", []string{"time", "endpoint_from"}, .95)+") SELECT time,endpoint_from,quantile AS latency FROM quantiles ORDER BY time DESC,endpoint_from", namespace)}
	}
	if unsupported || hasHistogram && hasScalar {
		return systemNote("check_latency", "Check latency", "httpcheck.duration has mixed or unsupported kinds or units. Report a consistent gauge/updown or histogram in s, ms, us or ns.")
	}

	for _, m := range metrics {
		if m.Name != "httpcheck.duration" {
			continue
		}
		if (m.Type == "gauge" || m.Type == "updown") && systemDurationFactor(m.Unit) != "" {
			return systemReachabilityPanels(namespace)[1]
		}
	}
	return systemNote("check_latency", "Check latency", "httpcheck.duration needs a gauge, updown or histogram with a supported duration unit (s, ms, us or ns). Unsupported or missing duration metadata cannot establish latency.")
}
func systemDurationFactor(unit string) string {
	switch unit {
	case "s":
		return "1000"
	case "ms":
		return "1"
	case "us", "µs":
		return ".001"
	case "ns":
		return ".000001"
	}
	return ""
}

const systemCheckSQL = `WITH raw AS (
 SELECT series_id,ingested_unix_nano,namespace,time,value,type,attributes['http.url']::VARCHAR AS url,
 attributes['http.method']::VARCHAR AS method,resource['host.name']::VARCHAR AS host,
 attributes['http.status_class']::VARCHAR AS class,attributes['http.status_code']::VARCHAR AS code
 FROM metrics WHERE time >= make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)::TIMESTAMPTZ_NS
 AND time < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS /* system namespace */ AND name='httpcheck.status' QUALIFY row_number() OVER (PARTITION BY series_id,time ORDER BY ingested_unix_nano DESC,value DESC)=1),
 keyed AS (SELECT *,to_json([namespace,url,method,host])::VARCHAR AS entity,
 coalesce(namespace,'')||' / '||coalesce(method,'method unknown')||' '||coalesce(url,'endpoint unknown')||' from '||coalesce(host,'vantage unknown') AS endpoint_from FROM raw),
 inventory AS (SELECT DISTINCT entity,endpoint_from FROM keyed WHERE $__window(time)),
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
 SELECT time,endpoint_from,failure,state FROM ranked ORDER BY current_failure DESC,time DESC,endpoint_from`

const systemDurationSQL = `WITH points AS (
 SELECT *,to_json([namespace,attributes['http.url']::VARCHAR,attributes['http.method']::VARCHAR,resource['host.name']::VARCHAR])::VARCHAR AS entity,
 namespace||' / '||coalesce(attributes['http.method']::VARCHAR,'method unknown')||' '||coalesce(attributes['http.url']::VARCHAR,'endpoint unknown')||' from '||coalesce(resource['host.name']::VARCHAR,'vantage unknown') AS endpoint_from,
 value*CASE unit WHEN 's' THEN 1000 WHEN 'ms' THEN 1 WHEN 'us' THEN .001 WHEN 'µs' THEN .001 WHEN 'ns' THEN .000001 END AS duration_ms
 FROM metrics WHERE time >= make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS AND time < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS /* system namespace */
 AND name='httpcheck.duration' AND type IN ('gauge','updown') AND isfinite(value)
 QUALIFY row_number() OVER (PARTITION BY series_id,time ORDER BY ingested_unix_nano DESC,value DESC,start_time DESC NULLS LAST)=1),
 inventory AS (SELECT DISTINCT series_id,endpoint_from FROM points WHERE $__window(time)),
 buckets AS (SELECT b::TIMESTAMPTZ_NS AS time,least((b+INTERVAL '1 minute')::TIMESTAMPTZ_NS,$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS) AS at
 FROM generate_series(time_bucket(INTERVAL '1 minute',$__from::TIMESTAMP_NS),$__to::TIMESTAMP_NS,INTERVAL '1 minute') t(b) WHERE b::TIMESTAMPTZ_NS < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 grid AS (SELECT * FROM inventory CROSS JOIN buckets)
 SELECT g.time,g.endpoint_from,sum(CASE WHEN p.time>=make_timestamp_ns(epoch_ns(g.at::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS THEN p.duration_ms END)::DOUBLE AS latency
 FROM grid g ASOF LEFT JOIN points p ON g.series_id=p.series_id AND g.at>p.time GROUP BY g.time,g.endpoint_from ORDER BY g.time DESC,g.endpoint_from`

func systemServicePanels(namespace string) []Panel {
	where := []string{}
	if namespace != "" {
		where = append(where, "namespace = "+sqlString(namespace))
	}
	return []Panel{{ID: "service_health", Title: "Is anything failing or slow?", Viz: "health", Width: 12, Height: "m", Query: &Query{From: "spans", Where: where}}, {ID: "service_map", Title: "Services and external dependencies", Viz: "service_map", Width: 12, Height: "l", Query: &Query{From: "spans", Where: where}}}
}

func systemCapacityPanels(namespace string, hosts, containers bool) []Panel {
	if !hosts && !containers {
		return []Panel{systemMissing("capacity_missing", "Is anything running out?", "hostmetrics` and `docker_stats")}
	}
	var panels []Panel
	if hosts {
		panels = append(panels, systemCapacityPanel("hosts", "Is anything running out? Hosts", systemHostSQL(), namespace))
	} else {
		panels = append(panels, systemMissing("hosts_missing", "Is anything running out? Hosts", "hostmetrics"))
	}
	if containers {
		panels = append(panels, systemCapacityPanel("containers", "Containers", systemContainerSQL(), namespace))
	} else {
		panels = append(panels, systemMissing("containers_missing", "Containers", "docker_stats"))
	}
	return panels
}
func systemCapacityPanel(id, title, text, namespace string) Panel {
	columns := []ColumnFormat{{Field: "status", Format: "status"}}
	for _, field := range []string{"cpu_percent", "memory_percent"} {
		columns = append(columns, ColumnFormat{Field: field, Format: "unit", Unit: "percent"})
	}
	if id == "hosts" {
		columns = append(columns, ColumnFormat{Field: "filesystem_percent", Format: "unit", Unit: "percent"})
	}
	return Panel{ID: id, Title: title, Viz: "table", Width: 12, Height: "m", SQL: systemScopedSQL(text, namespace), Better: "lower", Thresholds: []Threshold{{Value: 80, Status: "warn"}, {Value: 90, Status: "bad"}}, Options: &Options{Columns: columns}}
}

// One finite, retry-deduplicated latest-point stage shared by both tables.
func systemCapacityCTE(containers bool) string {
	names := "'system.cpu.time','system.memory.usage','system.filesystem.usage'"
	identity := "host IS NOT NULL AND host<>''"
	entity := "host"
	priority := "CASE name WHEN 'system.cpu.time' THEN 1 WHEN 'system.memory.usage' THEN 2 ELSE 3 END"
	if containers {
		identity += " AND container_id IS NOT NULL AND container_id<>''"
		names = "'container.cpu.utilization','container.memory.usage.total','container.memory.usage.limit'"
		entity = "to_json([host,container_id])::VARCHAR"
		priority = "CASE name WHEN 'container.cpu.utilization' THEN 1 WHEN 'container.memory.usage.total' THEN 2 ELSE 3 END"
	}
	return `WITH raw AS (SELECT *,resource['host.name']::VARCHAR AS host,resource['container.id']::VARCHAR AS container_id,
 resource['container.name']::VARCHAR AS container_name,attributes['state']::VARCHAR AS state,
 attributes['mountpoint']::VARCHAR AS mountpoint,attributes['device']::VARCHAR AS device
 FROM metrics WHERE time >= make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)::TIMESTAMPTZ_NS AND time < $__to::TIMESTAMP_NS::TIMESTAMPTZ_NS /* system namespace */ AND name IN (` + names + `)),
 keyed AS (SELECT *,` + entity + ` AS entity,` + priority + ` AS priority FROM raw WHERE ` + identity + `),
 inventory AS (SELECT namespace,entity,any_value(host) AS host,any_value(container_id) AS container_id,arg_max(container_name,time) AS container FROM keyed WHERE $__window(time) GROUP BY namespace,entity),
 witness_family AS (SELECT namespace,entity,min(priority) AS priority FROM keyed GROUP BY namespace,entity),
 witness AS (SELECT DISTINCT k.namespace,k.entity,k.time FROM keyed k JOIN witness_family f USING(namespace,entity,priority)),
 gaps AS (SELECT *,epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(lag(time) OVER (PARTITION BY namespace,entity ORDER BY time)::TIMESTAMP_NS)::HUGEINT AS gap_ns FROM witness),
 cadence AS (SELECT namespace,entity,max(time) AS last_seen,arg_max(gap_ns,time) FILTER (WHERE gap_ns>0) AS interval_ns FROM gaps GROUP BY namespace,entity),
 unique_points AS (SELECT * FROM keyed WHERE isfinite(value) QUALIFY row_number() OVER (PARTITION BY series_id,time ORDER BY ingested_unix_nano DESC,value DESC,start_time DESC NULLS LAST)=1),
 latest AS (SELECT * FROM unique_points WHERE time >= make_timestamp_ns(epoch_ns($__to::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS AND type IN ('gauge','updown') QUALIFY row_number() OVER (PARTITION BY series_id ORDER BY time DESC)=1)`
}

const systemReporting = `CASE WHEN i.host IS NULL OR i.host='' /* identity */ THEN 'stable resource identity missing'
 WHEN c.interval_ns IS NULL THEN 'reporting interval unknown'
 WHEN epoch_ns($__to::TIMESTAMP_NS)::HUGEINT-epoch_ns(c.last_seen::TIMESTAMP_NS)::HUGEINT >= 2*c.interval_ns THEN 'not reporting' ELSE 'reporting' END`

func systemHostSQL() string {
	return systemCapacityCTE(false) + `,
 cpu_previous AS (SELECT *,lag(value) OVER w AS previous_value,lag(time) OVER w AS previous_time,lag(start_time) OVER w AS previous_start FROM unique_points WHERE name='system.cpu.time' AND type='counter' WINDOW w AS (PARTITION BY series_id ORDER BY time)),
 cpu_delta AS (SELECT *,CASE WHEN temporality='delta' OR value<previous_value OR (start_time IS NOT NULL AND previous_start IS NOT NULL AND start_time<>previous_start) THEN value ELSE value-previous_value END AS increase,
 CASE WHEN temporality='delta' AND start_time IS NOT NULL AND start_time<time THEN start_time ELSE previous_time END AS elapsed_from FROM cpu_previous WHERE temporality IN ('delta','cumulative') AND (temporality='delta' OR previous_time IS NOT NULL)),
 cpu_overlap AS (SELECT *, (epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(elapsed_from::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9 AS elapsed_s,
 greatest(0,(epoch_ns(least(time,$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS)::TIMESTAMP_NS)::HUGEINT-epoch_ns(greatest(elapsed_from,$__from::TIMESTAMP_NS::TIMESTAMPTZ_NS)::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9) AS overlap_s FROM cpu_delta WHERE elapsed_from<time),
 cpu_intervals AS (SELECT *,CASE WHEN elapsed_s<=600 THEN increase*overlap_s/elapsed_s WHEN $__window(time) THEN increase END AS rate_share,
 CASE WHEN elapsed_s<=600 THEN overlap_s WHEN $__window(time) THEN elapsed_s END AS covered_s FROM cpu_overlap),
 cpu_series AS (SELECT namespace,entity,series_id,any_value(state) AS state,
 CASE WHEN max(time)<make_timestamp_ns(epoch_ns($__to::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS THEN NULL
 ELSE coalesce(sum(rate_share) FILTER (WHERE covered_s>0)/nullif(sum(covered_s) FILTER (WHERE covered_s>0),0),arg_max(increase/elapsed_s,time)) END AS cpu_rate FROM cpu_intervals GROUP BY namespace,entity,series_id),
 cpu AS (SELECT namespace,entity,100.0*sum(cpu_rate) FILTER (WHERE state<>'idle')/nullif(sum(cpu_rate),0) AS cpu_percent FROM cpu_series GROUP BY namespace,entity),
 memory AS (SELECT namespace,entity,100.0*sum(value) FILTER (WHERE state='used')/nullif(sum(value),0) AS memory_percent FROM latest WHERE name='system.memory.usage' GROUP BY namespace,entity),
 mounts AS (SELECT namespace,entity,mountpoint,device,sum(value) FILTER (WHERE state IN ('used','reserved')) AS used_bytes,sum(value) AS total_bytes FROM latest WHERE name='system.filesystem.usage' GROUP BY namespace,entity,mountpoint,device),
 fullest AS (SELECT *,100.0*used_bytes/nullif(total_bytes,0) AS filesystem_percent FROM mounts QUALIFY row_number() OVER (PARTITION BY namespace,entity ORDER BY filesystem_percent DESC NULLS LAST,mountpoint,device)=1),
 results AS (SELECT i.namespace,i.host,` + strings.ReplaceAll(systemReporting, "/* identity */", "") + ` AS reporting,cpu.cpu_percent,m.memory_percent,f.filesystem_percent,f.mountpoint,f.used_bytes,f.total_bytes,c.last_seen FROM inventory i LEFT JOIN cadence c USING(namespace,entity) LEFT JOIN cpu USING(namespace,entity) LEFT JOIN memory m USING(namespace,entity) LEFT JOIN fullest f USING(namespace,entity))
 SELECT namespace,host,CASE WHEN reporting<>'reporting' THEN 100 ELSE greatest(cpu_percent,memory_percent,filesystem_percent) END::DOUBLE AS status,reporting,cpu_percent,memory_percent,filesystem_percent,mountpoint,used_bytes,total_bytes,last_seen,
 CASE WHEN memory_percent IS NULL OR filesystem_percent IS NULL OR cpu_percent IS NULL THEN 'measurement missing, unsupported, stale or denominator unavailable' END AS measurement
 FROM results ORDER BY (reporting<>'reporting') DESC,status DESC NULLS LAST,namespace,host`
}
func systemContainerSQL() string {
	return systemCapacityCTE(true) + `, values_by_container AS (SELECT namespace,entity,
 sum(value*CASE unit WHEN '%' THEN 1 WHEN '1' THEN 100 END) FILTER (WHERE name='container.cpu.utilization') AS cpu_percent,
 sum(value) FILTER (WHERE name='container.memory.usage.total') AS total_bytes,sum(value) FILTER (WHERE name='container.memory.usage.limit') AS limit_bytes FROM latest GROUP BY namespace,entity),
 results AS (SELECT i.namespace,i.host,coalesce(i.container,i.container_id) AS container,` + strings.ReplaceAll(systemReporting, "/* identity */", "OR i.container_id IS NULL OR i.container_id=''") + ` AS reporting,v.cpu_percent,100.0*v.total_bytes/nullif(v.limit_bytes,0) AS memory_percent,v.total_bytes,v.limit_bytes,c.last_seen FROM inventory i LEFT JOIN cadence c USING(namespace,entity) LEFT JOIN values_by_container v USING(namespace,entity))
 SELECT namespace,host,container,CASE WHEN reporting<>'reporting' THEN 100 ELSE greatest(cpu_percent,memory_percent) END::DOUBLE AS status,reporting,cpu_percent,memory_percent,total_bytes,limit_bytes,last_seen,
 CASE WHEN limit_bytes IS NULL OR limit_bytes=0 THEN 'memory limit not reported' WHEN cpu_percent IS NULL OR memory_percent IS NULL THEN 'measurement missing, unsupported or stale' END AS measurement
 FROM results ORDER BY (reporting<>'reporting') DESC,status DESC NULLS LAST,namespace,host,container`
}

const systemVersionObservations = `SELECT namespace,service,resource['service.version']::VARCHAR AS version,resource['process.runtime.name']::VARCHAR AS runtime_name,resource['process.runtime.version']::VARCHAR AS runtime_version,resource['telemetry.sdk.language']::VARCHAR AS sdk_language,resource['telemetry.sdk.version']::VARCHAR AS sdk_version,start_time AS seen FROM spans WHERE $__window(start_time) /* system namespace */
 UNION ALL SELECT namespace,service,resource['service.version']::VARCHAR,resource['process.runtime.name']::VARCHAR,resource['process.runtime.version']::VARCHAR,resource['telemetry.sdk.language']::VARCHAR,resource['telemetry.sdk.version']::VARCHAR,time FROM logs WHERE $__window(time) /* system namespace */
 UNION ALL SELECT namespace,service,resource['service.version']::VARCHAR,resource['process.runtime.name']::VARCHAR,resource['process.runtime.version']::VARCHAR,resource['telemetry.sdk.language']::VARCHAR,resource['telemetry.sdk.version']::VARCHAR,time FROM metrics WHERE $__window(time) /* system namespace */`

func systemVersionPanel(namespace string) Panel {
	return Panel{ID: "versions", Title: "What changed?", Description: "First seen in the selected range.", Viz: "table", Width: 12, Height: "m", SQL: systemScopedSQL(`WITH observations AS (`+systemVersionObservations+`) SELECT namespace,service,version,min(seen) AS first_seen,runtime_name,runtime_version,sdk_language,sdk_version FROM observations WHERE version IS NOT NULL AND version<>'' GROUP BY namespace,service,version,runtime_name,runtime_version,sdk_language,sdk_version ORDER BY first_seen DESC,namespace,service,version`, namespace)}
}

// Histogram duration uses the same reset, rebin and overlap rules as Phase A,
// with its existing interpolation helper. No value carry applies to quantiles.
const systemDurationHistogramSQL = `WITH decoded AS (
 SELECT *,TRY(from_json(hist_bounds_json,'["DOUBLE"]')) AS bounds,TRY(from_json(hist_counts_json,'["UBIGINT"]')) AS counts,
 namespace||' / '||coalesce(attributes['http.method']::VARCHAR,'method unknown')||' '||coalesce(attributes['http.url']::VARCHAR,'endpoint unknown')||' from '||coalesce(resource['host.name']::VARCHAR,'vantage unknown') AS endpoint_from,
 CASE unit WHEN 's' THEN 1000 WHEN 'ms' THEN 1 WHEN 'us' THEN .001 WHEN 'µs' THEN .001 WHEN 'ns' THEN .000001 END AS factor
 FROM metrics WHERE ($__window(time) OR time>=make_timestamp_ns(epoch_ns($__from::TIMESTAMP_NS)-600000000000)::TIMESTAMPTZ_NS) AND time<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS /* system namespace */ AND name='httpcheck.duration' AND type='histogram'),
 points AS (SELECT * FROM decoded WHERE isfinite(hist_sum) AND hist_count>=0 AND bounds IS NOT NULL AND counts IS NOT NULL AND len(counts)=len(bounds)+1
 AND coalesce(list_bool_and(list_transform(bounds,lambda x:isfinite(x))),true) AND coalesce(list_bool_and(list_transform(range(2,len(bounds)+1),lambda i:bounds[i-1]<bounds[i])),true)
 QUALIFY row_number() OVER (PARTITION BY series_id,time ORDER BY ingested_unix_nano DESC,value DESC,hist_count DESC,hist_sum DESC,hist_counts_json DESC,hist_bounds_json DESC,start_time DESC NULLS LAST)=1),
 previous AS (SELECT *,lag(time) OVER w AS previous_time,lag(start_time) OVER w AS previous_start,lag(hist_count) OVER w AS previous_count,lag(bounds) OVER w AS previous_bounds,lag(counts) OVER w AS previous_counts FROM points WINDOW w AS (PARTITION BY series_id ORDER BY time)),
 rebinned AS (SELECT *,CASE WHEN bounds<>previous_bounds THEN list_transform(range(1,len(counts)+1),lambda j:list_sum(list_transform(range(1,len(previous_counts)+1),lambda i:
 CASE WHEN coalesce(bounds[j-1],'-Infinity'::DOUBLE)<=coalesce(previous_bounds[i-1],'-Infinity'::DOUBLE) AND coalesce(previous_bounds[i],'Infinity'::DOUBLE)<=coalesce(bounds[j],'Infinity'::DOUBLE) THEN previous_counts[i]::HUGEINT ELSE 0::HUGEINT END))) ELSE list_transform(previous_counts,lambda n:n::HUGEINT) END AS old_counts FROM previous),
 resets AS (SELECT *,hist_count<previous_count OR (start_time IS NOT NULL AND previous_start IS NOT NULL AND start_time<>previous_start) OR (bounds<>previous_bounds AND list_sum(old_counts)<>list_sum(list_transform(previous_counts,lambda n:n::HUGEINT))) OR NOT list_bool_and(list_transform(range(1,len(counts)+1),lambda i:counts[i]::HUGEINT-old_counts[i]>=0)) AS reset,
 CASE WHEN temporality='delta' AND start_time IS NOT NULL AND start_time<time THEN start_time ELSE previous_time END AS elapsed_from FROM rebinned),
 deltas AS (SELECT *,list_transform(range(1,len(counts)+1),lambda i:CASE WHEN temporality='delta' OR reset THEN counts[i]::HUGEINT ELSE counts[i]::HUGEINT-old_counts[i] END) AS increases,
 (epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(elapsed_from::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9 AS elapsed_s FROM resets WHERE temporality IN ('delta','cumulative') AND (temporality='delta' OR previous_time IS NOT NULL)),
 buckets AS (SELECT b::TIMESTAMPTZ_NS AS bucket_time,greatest(b::TIMESTAMPTZ_NS,$__from::TIMESTAMP_NS::TIMESTAMPTZ_NS) AS lo,least((b+INTERVAL '1 minute')::TIMESTAMPTZ_NS,$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS) AS hi FROM generate_series(time_bucket(INTERVAL '1 minute',$__from::TIMESTAMP_NS),$__to::TIMESTAMP_NS,INTERVAL '1 minute') t(b) WHERE b::TIMESTAMPTZ_NS<$__to::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 hist_overlaps AS (SELECT d.*,b.bucket_time,CASE WHEN elapsed_s>0 AND elapsed_s<=600 THEN (epoch_ns(least(d.time,b.hi)::TIMESTAMP_NS)::HUGEINT-epoch_ns(greatest(elapsed_from,b.lo)::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9/elapsed_s ELSE 1 END AS share
 FROM deltas d JOIN buckets b ON (elapsed_s>0 AND elapsed_s<=600 AND d.time>b.lo AND elapsed_from<b.hi) OR ((elapsed_s IS NULL OR elapsed_s>600) AND d.time>=b.lo AND d.time<b.hi)),
 weights_input AS (SELECT bucket_time AS time,endpoint_from,unnest(list_append(list_transform(bounds,lambda x:x*factor),NULL::DOUBLE)) AS hi,unnest(list_transform(increases,lambda n:n::DOUBLE*share)) AS n FROM hist_overlaps)`
