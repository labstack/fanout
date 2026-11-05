package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Scalar presence checks must precede attribute-reading statements: on the
// glob-backed snapshot the kind filter can remain above the Parquet scan.
func insertEdgeBuckets(ctx context.Context, tx *sql.Tx, lo, hi int64) (sql.Result, error) {
	var messaging bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM spans
 WHERE ingested_unix_nano > ?::BIGINT AND ingested_unix_nano <= ?::BIGINT
 AND start_time IS NOT NULL AND kind IN ('SPAN_KIND_PRODUCER','SPAN_KIND_CONSUMER'))`, lo, hi).Scan(&messaging); err != nil {
		return nil, err
	}
	statement := edgePassBucketsSelectSQL
	if !messaging {
		statement = edgePassBucketsNoMessagingSQL
	}
	return tx.ExecContext(ctx, "INSERT INTO read_edge_buckets "+statement, lo, hi)
}

func withoutEdgeMessaging(statement string) string {
	statement = strings.Replace(statement, edgeProducerAggSQL, edgeEmptyProducersSQL, 1)
	return strings.Replace(statement, edgeConsumerAggSQL, edgeEmptyConsumersSQL, 1)
}

const edgeIncomingMessagesSQL = `  SELECT namespace, start_time, kind, messaging_destination, messaging_system
  FROM spans
  WHERE ingested_unix_nano > $1::BIGINT
    AND ingested_unix_nano <= $2::BIGINT
    AND start_time IS NOT NULL
    AND kind IN ('SPAN_KIND_PRODUCER','SPAN_KIND_CONSUMER')`

var edgeRecentMessagesSQL = fmt.Sprintf(`  SELECT namespace, start_time, kind, messaging_destination, messaging_system
  FROM spans
  WHERE ingested_unix_nano > ($1::BIGINT - %[1]d)
    AND ingested_unix_nano <= $1::BIGINT
    AND start_time IS NOT NULL
    AND kind IN ('SPAN_KIND_PRODUCER','SPAN_KIND_CONSUMER')`, int64(edgeArrivalGap))

const edgeEmptyMessagesSQL = `SELECT NULL::VARCHAR AS namespace,NULL::TIMESTAMPTZ_NS AS start_time,NULL::VARCHAR AS kind,NULL::VARCHAR AS messaging_destination,NULL::VARCHAR AS messaging_system WHERE false`

var edgePassBucketsNoMessagingSQL = strings.Replace(strings.Replace(edgePassBucketsSelectSQL, edgeIncomingMessagesSQL, edgeEmptyMessagesSQL, 1), edgeRecentMessagesSQL, edgeEmptyMessagesSQL, 1)

const edgeProducerAggSQL = `  SELECT DISTINCT
    s.namespace,
    date_trunc('minute', s.start_time::TIMESTAMP_NS) AS bucket,
    s.service,
    s.messaging_destination AS destination,
    s.messaging_system AS msg_system
  FROM spans s
  WHERE s.kind = 'SPAN_KIND_PRODUCER'
    AND s.start_time >= ((SELECT lo FROM bounds))::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.start_time < ((SELECT hi FROM bounds) + INTERVAL 1 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.service IS NOT NULL
    AND s.service != ''
    AND s.messaging_destination IS NOT NULL`

const edgeConsumerAggSQL = `  SELECT
    s.namespace,
    date_trunc('minute', s.start_time::TIMESTAMP_NS) AS bucket,
    s.service,
    s.messaging_destination AS destination,
    s.messaging_system AS msg_system,
    COUNT(*) AS calls
  FROM spans s
  WHERE s.kind = 'SPAN_KIND_CONSUMER'
    AND s.start_time >= ((SELECT lo FROM bounds))::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.start_time < ((SELECT hi FROM bounds) + INTERVAL 1 MINUTE)::TIMESTAMP_NS::TIMESTAMPTZ_NS
    AND s.service IS NOT NULL
    AND s.service != ''
    AND s.messaging_destination IS NOT NULL
  GROUP BY s.namespace, date_trunc('minute', s.start_time::TIMESTAMP_NS), s.service,
    s.messaging_destination,
    s.messaging_system`

const edgeEmptyProducersSQL = `SELECT NULL::VARCHAR AS namespace,NULL::TIMESTAMP_NS AS bucket,NULL::VARCHAR AS service,NULL::VARCHAR AS destination,NULL::VARCHAR AS msg_system WHERE false`
const edgeEmptyConsumersSQL = `SELECT NULL::VARCHAR AS namespace,NULL::TIMESTAMP_NS AS bucket,NULL::VARCHAR AS service,NULL::VARCHAR AS destination,NULL::VARCHAR AS msg_system,0::BIGINT AS calls WHERE false`
