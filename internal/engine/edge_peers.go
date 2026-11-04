package engine

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Resolve candidates once before the history lookup. A MARK join preserves the
// small build side, but unlike a semi-join cannot stop probing when it is empty.
// The explicit empty case avoids scanning all retained spans without peers.
func prepareEdgePeerCandidates(ctx context.Context, tx *sql.Tx, lo, hi time.Time) (bool, error) {
	if _, err := tx.ExecContext(ctx, createEdgePeerCandidatesSQL, lo, hi); err != nil {
		return false, err
	}
	var havePeers bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM edge_pass_peer_candidates)`).Scan(&havePeers)
	return havePeers, err
}

const edgePeerCandidatesSQL = ` SELECT s.namespace,s.trace_id,s.span_id,s.service,s.peer_service,s.duration_ms,s.status,
 date_trunc('minute',s.start_time::TIMESTAMP_NS) AS bucket
 FROM spans s
 WHERE ((s.namespace,date_trunc('minute',s.start_time::TIMESTAMP_NS)) IN (SELECT (namespace,bucket) FROM affected WHERE namespace IS NOT NULL)) IS TRUE
 AND s.start_time >= ((SELECT lo FROM bounds))::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND s.start_time < ((SELECT hi FROM bounds)+INTERVAL 1 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
 AND s.kind='SPAN_KIND_CLIENT' AND coalesce(s.service,'')<>''
 AND coalesce(s.peer_service,'')<>'' AND s.peer_service<>s.service`

const edgePeerChildrenSQL = ` SELECT DISTINCT c.namespace,c.trace_id,c.parent_span_id,c.service
 FROM spans c WHERE ((c.namespace,c.trace_id,c.parent_span_id) IN (SELECT (namespace,trace_id,span_id) FROM peer_candidates WHERE namespace IS NOT NULL AND trace_id IS NOT NULL AND span_id IS NOT NULL)) IS TRUE`

const createEdgePeerCandidatesSQL = `CREATE TEMP TABLE edge_pass_peer_candidates AS ` + edgeAffectedBucketsSQL + `,
bounds AS (SELECT MIN(bucket) AS lo, MAX(bucket) AS hi FROM affected)
` + edgePeerCandidatesSQL

// Candidates are already materialized in the transaction-local table. Reuse
// its scans without buffering a second copy of a nonempty candidate set.
var edgeRollupPreparedInsertSQL = strings.Replace(strings.Replace(edgeRollupInsertSQL, edgePeerCandidatesSQL, `SELECT * FROM edge_pass_peer_candidates`, 1), "peer_candidates AS MATERIALIZED (", "peer_candidates AS NOT MATERIALIZED (", 1)
var edgeRollupNoPeersInsertSQL = strings.Replace(edgeRollupPreparedInsertSQL, edgePeerChildrenSQL, `SELECT NULL::VARCHAR AS namespace,NULL::VARCHAR AS trace_id,NULL::VARCHAR AS parent_span_id,NULL::VARCHAR AS service WHERE false`, 1)
