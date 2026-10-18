package query

import (
	"strings"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

// Every signal partitions the captured files independently. Whole cached files
// are exact only inside the logical window; boundary and unacknowledged files
// must supply actual observations, rather than overlapping resource envelopes.
func (d *Duck) resourceInventorySource(batches []telemetry.BatchMetadata, markers map[string]bool, window queryrows.Window) string {
	retained := window.RetainedInventory
	var contributions []string
	for _, signal := range []string{"spans", "logs", "metrics"} {
		var interior []string
		var raw []telemetry.BatchMetadata
		for _, batch := range batches {
			bounds, count := batchTime(batch, signal)
			if count == 0 || !retained && !overlapping(batch, signal, window) {
				continue
			}
			if markers[batch.ID] && (retained || bounds.Known && bounds.MinNanos >= window.Start.UnixNano() && bounds.MaxNanos < window.End.UnixNano()) {
				interior = append(interior, batch.ID)
			} else {
				raw = append(raw, batch)
			}
		}
		if len(interior) > 0 {
			contributions = append(contributions, `SELECT signal,namespace,service,resource,first_seen,last_seen FROM read_resource_inventory WHERE signal=`+sqlLiteral(signal)+` AND batch_id IN (`+idsSQL(interior)+`)`)
		}
		if len(raw) > 0 {
			clock := "time"
			if signal == "spans" {
				clock = "start_time"
			}
			predicate := "true"
			if !retained {
				predicate = windowPredicate(clock, window)
			}
			contributions = append(contributions, `SELECT `+sqlLiteral(signal)+` AS signal,namespace,service,resource,min(`+clock+`) AS first_seen,max(`+clock+`) AS last_seen FROM (`+cleanSource(signal, d.snapshotSource(signal, raw, batches))+`) WHERE `+predicate+` GROUP BY namespace,service,resource`)
		}
	}
	if len(contributions) == 0 {
		return emptyResourceInventory
	}
	scope := ""
	if window.Namespace != "" {
		scope += " AND namespace=" + sqlLiteral(window.Namespace)
	}
	if window.Service != "" {
		scope += " AND service=" + sqlLiteral(window.Service)
	}
	return `SELECT signal,namespace,service,resource,min(first_seen) AS first_seen,max(last_seen) AS last_seen FROM (` + strings.Join(contributions, " UNION ALL ") + `) WHERE true` + scope + ` GROUP BY signal,namespace,service,resource`
}
