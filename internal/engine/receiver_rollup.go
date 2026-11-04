package engine

import "fmt"

// Only receiver-defined dimensions participate in the key. Arbitrary OTLP
// attributes, resources and series IDs cannot multiply summary populations.
const receiverNames = "'httpcheck.status','httpcheck.duration','system.cpu.time','system.memory.usage','system.filesystem.usage','container.cpu.utilization','container.cpu.limit','container.memory.usage.total','container.memory.usage.limit'"

const receiverPointType = `STRUCT(time TIMESTAMPTZ_NS,value DOUBLE,start_time TIMESTAMPTZ_NS,ingested_unix_nano BIGINT)`

const createReceiverRollupTable = `CREATE TABLE IF NOT EXISTS receiver_rollup (
 namespace VARCHAR,bucket TIMESTAMP_NS,service VARCHAR,series_id VARCHAR,
 name VARCHAR,type VARCHAR,temporality VARCHAR,unit VARCHAR,scope_name VARCHAR,
 resource VARIANT,attributes VARIANT,
 host VARCHAR,container_id VARCHAR,container_name VARCHAR,state VARCHAR,cpu VARCHAR,mountpoint VARCHAR,device VARCHAR,url VARCHAR,method VARCHAR,class VARCHAR,code VARCHAR,
 first_point ` + receiverPointType + `,last_point ` + receiverPointType + `,previous_point ` + receiverPointType + `,
 first_finite_point ` + receiverPointType + `,last_finite_point ` + receiverPointType + `,previous_finite_point ` + receiverPointType + `,
 counter_increase DOUBLE,counter_seconds DOUBLE
)`

const createResourceRollupTable = `CREATE TABLE IF NOT EXISTS resource_rollup (
 namespace VARCHAR,bucket TIMESTAMP_NS,service VARCHAR,signal VARCHAR,
 resource VARIANT,first_seen TIMESTAMPTZ_NS,last_seen TIMESTAMPTZ_NS
)`

func receiverRollupInsertSQL(keys string) string {
	return fmt.Sprintf(`INSERT INTO receiver_rollup
 WITH projected AS (
 SELECT namespace,date_trunc('minute',time::TIMESTAMP_NS) bucket,service,name,type,temporality,unit,scope_name,
 struct_pack("host.name" := resource['host.name']::VARCHAR,"container.id" := _r_container_id,"container.name" := resource['container.name']::VARCHAR)::VARIANT resource,
 struct_pack(state := attributes['state']::VARCHAR,cpu := attributes['cpu']::VARCHAR,mountpoint := attributes['mountpoint']::VARCHAR,device := attributes['device']::VARCHAR,
 "http.url" := attributes['http.url']::VARCHAR,"http.method" := attributes['http.method']::VARCHAR,"http.status_class" := attributes['http.status_class']::VARCHAR,"http.status_code" := attributes['http.status_code']::VARCHAR)::VARIANT attributes,
 resource['host.name']::VARCHAR host,_r_container_id container_id,resource['container.name']::VARCHAR container_name,
 attributes['state']::VARCHAR state,attributes['cpu']::VARCHAR cpu,attributes['mountpoint']::VARCHAR mountpoint,attributes['device']::VARCHAR device,
 attributes['http.url']::VARCHAR url,attributes['http.method']::VARCHAR method,attributes['http.status_class']::VARCHAR class,attributes['http.status_code']::VARCHAR code,
 time,value,start_time,ingested_unix_nano
 FROM metrics m WHERE %s AND name IN (%s)
 AND time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS),
 keyed AS (SELECT *,sha256(to_json([namespace,service,name,type,temporality,unit,scope_name,to_json(resource)::VARCHAR,to_json(attributes)::VARCHAR])) series_id FROM projected),
 dedup AS (SELECT * FROM keyed QUALIFY row_number() OVER (PARTITION BY series_id,time ORDER BY ingested_unix_nano DESC,value DESC,start_time DESC NULLS LAST)=1),
 ordered AS (SELECT *,struct_pack(time:=time,value:=value,start_time:=start_time,ingested_unix_nano:=ingested_unix_nano) point,
 last_value(CASE WHEN isfinite(value) THEN struct_pack(value:=value,time:=time,start_time:=start_time) END IGNORE NULLS) OVER w previous_finite
 FROM dedup WINDOW w AS (PARTITION BY series_id,bucket ORDER BY time ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING)),
 grouped AS (SELECT namespace,bucket,service,series_id,name,type,temporality,unit,scope_name,resource,attributes,host,container_id,container_name,state,cpu,mountpoint,device,url,method,class,code,
 arg_min(point,time) first_point,arg_max(point,time) last_point,
 arg_max(point,time,2)[2] previous_point,
 arg_min(point,time) FILTER (WHERE isfinite(value)) first_finite_point,
 arg_max(point,time) FILTER (WHERE isfinite(value)) last_finite_point,
 (arg_max(point,time,2) FILTER (WHERE isfinite(value)))[2] previous_finite_point,
 sum(CASE WHEN isfinite(value) AND isfinite(previous_finite.value) THEN CASE WHEN temporality='delta' OR value<previous_finite.value OR (start_time IS NOT NULL AND previous_finite.start_time IS NOT NULL AND start_time<>previous_finite.start_time) THEN value ELSE value-previous_finite.value END END) counter_increase,
 sum(CASE WHEN isfinite(value) AND isfinite(previous_finite.value) THEN epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(CASE WHEN temporality='delta' AND start_time<time THEN start_time ELSE previous_finite.time END::TIMESTAMP_NS)::HUGEINT END)::DOUBLE/1e9 counter_seconds
 FROM ordered GROUP BY namespace,bucket,service,series_id,name,type,temporality,unit,scope_name,resource,attributes,host,container_id,container_name,state,cpu,mountpoint,device,url,method,class,code)
 SELECT * FROM grouped`, keys, receiverNames)
}

func resourceRollupInsertSQL(signal, clock, keys string) string {
	return fmt.Sprintf(`INSERT INTO resource_rollup
 WITH projected AS (SELECT namespace,date_trunc('minute',%[1]s::TIMESTAMP_NS) bucket,service,
 struct_pack("host.name" := resource['host.name']::VARCHAR,"service.version" := _r_service_version,
 "process.runtime.name" := resource['process.runtime.name']::VARCHAR,"process.runtime.version" := resource['process.runtime.version']::VARCHAR,
 "telemetry.sdk.language" := resource['telemetry.sdk.language']::VARCHAR,"telemetry.sdk.version" := resource['telemetry.sdk.version']::VARCHAR)::VARIANT identity,%[1]s seen
 FROM %[3]s m WHERE %[4]s AND %[1]s>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND %[1]s<?::TIMESTAMP_NS::TIMESTAMPTZ_NS)
 SELECT namespace,bucket,service,%[2]s,identity,min(seen),max(seen) FROM projected GROUP BY namespace,bucket,service,identity`, clock, sqlLiteral(signal), signal, keys)
}

// First/last witnesses preserve cadence even for nonfinite scrapes; finite
// witnesses retain the last usable gauge and counter boundaries. Both sets
// have fixed size per series-minute. Counter sums preserve interior resets.
const createReceiverPointsView = `CREATE OR REPLACE VIEW receiver_points AS
 SELECT namespace,bucket,service,series_id,name,type,temporality,unit,scope_name,resource,attributes,host,container_id,container_name,state,cpu,mountpoint,device,url,method,class,code,
 p.time,p.value,p.start_time,p.ingested_unix_nano
 FROM (SELECT *,
 CASE WHEN name='httpcheck.status' THEN first_point ELSE first_finite_point END a,
 CASE WHEN name='httpcheck.status' THEN previous_point ELSE previous_finite_point END b,
 CASE WHEN name='httpcheck.status' THEN last_point ELSE last_finite_point END c
 FROM receiver_rollup) r,
 unnest([a,CASE WHEN b.time<>a.time THEN b END,CASE WHEN c.time<>a.time THEN c END]) t(p) WHERE p IS NOT NULL`
