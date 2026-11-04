package engine

import "fmt"

const receiverObservationType = `STRUCT(time TIMESTAMPTZ_NS,flag INTEGER)`

// Collapse reporting witnesses across CPUs, states, mounts and HTTP status
// classes. Keeping local first/last witnesses needs no forward invalidation:
// readers bridge consecutive minutes after late data repairs its own bucket.
const createReceiverEntityRollupTable = `CREATE TABLE IF NOT EXISTS receiver_entity_rollup (
 namespace VARCHAR,bucket TIMESTAMP_NS,service VARCHAR,receiver VARCHAR,family VARCHAR,
 entity VARCHAR,host VARCHAR,container_id VARCHAR,container_name VARCHAR,endpoint_from VARCHAR,
 first_observation ` + receiverObservationType + `,previous_observation ` + receiverObservationType + `,last_observation ` + receiverObservationType + `
)`

func receiverEntityRollupInsertSQL(keys string) string {
	return fmt.Sprintf(`INSERT INTO receiver_entity_rollup
 WITH projected AS (SELECT m.namespace,m.bucket,m.service,m.name family,m.host,m.container_id,m.container_name,
 CASE WHEN m.name='httpcheck.status' THEN 'check' WHEN starts_with(m.name,'container.') THEN 'container' ELSE 'host' END receiver,
 CASE WHEN m.name='httpcheck.status' THEN sha256(to_json([m.namespace,m.url,m.method,m.host]))
 WHEN starts_with(m.name,'container.') THEN sha256(to_json([m.host,m.container_id])) ELSE m.host END entity,
 CASE WHEN m.name='httpcheck.status' THEN coalesce(m.namespace,'')||' / '||coalesce(m.method,'method unknown')||' '||coalesce(m.url,'endpoint unknown')||' from '||coalesce(m.host,'vantage unknown') END endpoint_from,
 p.time,
 CASE WHEN m.name<>'httpcheck.status' THEN 0
 WHEN m.url IS NULL OR m.url='' OR m.method IS NULL OR m.method='' OR m.host IS NULL OR m.host='' OR m.type NOT IN ('gauge','updown') OR NOT isfinite(p.value) OR (m.class IS NULL AND m.code IS NULL) THEN 2
 WHEN p.value>0 AND (m.class IN ('2xx','3xx') OR try_cast(m.code AS INTEGER) BETWEEN 200 AND 399) THEN 1 ELSE 0 END flag
 FROM receiver_rollup m,unnest([m.first_point,m.previous_point,m.last_point]) t(p)
 WHERE %s AND m.name<>'httpcheck.duration' AND p IS NOT NULL
 AND m.bucket>=?::TIMESTAMP_NS AND m.bucket<?::TIMESTAMP_NS),
 attempts AS (SELECT namespace,bucket,service,receiver,family,entity,host,container_id,
 arg_max(container_name,time) container_name,endpoint_from,time,max(flag) flag FROM projected
 GROUP BY namespace,bucket,service,receiver,family,entity,host,container_id,endpoint_from,time),
 observations AS (SELECT *,struct_pack(time:=time,flag:=flag) observation FROM attempts)
 SELECT namespace,bucket,service,receiver,family,entity,host,container_id,arg_max(container_name,time),endpoint_from,
 arg_min(observation,time),arg_max(observation,time,2)[2],arg_max(observation,time)
 FROM observations GROUP BY namespace,bucket,service,receiver,family,entity,host,container_id,endpoint_from`, keys)
}
