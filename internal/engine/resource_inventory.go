package engine

import (
	"strings"
	"time"

	"github.com/labstack/pitot/internal/queryrows"
	"github.com/labstack/pitot/internal/telemetry"
)

// Every signal partitions the captured files independently. Whole cached files
// are exact only inside the logical window. Only boundary files supply raw
// observations; missing interior contributions are disclosed as cache coverage.
func (d *Duck) resourceInventorySource(batches []telemetry.BatchMetadata, markers map[string]bool, window queryrows.Window) string {
	retained := window.RetainedInventory
	var contributions []string
	for _, s := range telemetry.Signals {
		signal := s.Name
		var interior, boundary []string
		var raw []telemetry.BatchMetadata
		for _, batch := range batches {
			bounds, count := batchTime(batch, signal)
			if count == 0 || !retained && !overlapping(batch, signal, window) {
				continue
			}
			if retained || bounds.Known && bounds.MinNanos >= window.Start.UnixNano() && bounds.MaxNanos < window.End.UnixNano() {
				if markers[batch.ID] {
					interior = append(interior, batch.ID)
				} else if retained {
					// Retained membership checks are not dashboard aggregates:
					// a newly published service must never be called unknown.
					raw = append(raw, batch)
				}
			} else if markers[batch.ID] {
				boundary = append(boundary, batch.ID)
			} else {
				raw = append(raw, batch)
			}
		}
		if len(interior) > 0 {
			contributions = append(contributions, `SELECT signal,namespace,service,resource,first_seen,last_seen FROM read_resource_inventory WHERE signal=`+sqlLiteral(signal)+` AND batch_id IN (`+idsSQL(interior)+`)`)
		}
		if len(boundary) > 0 {
			// Complete second contributions avoid decoding the portion of a
			// large compacted file strictly inside the window. Only its two
			// partial seconds need exact raw observation bounds.
			lower := window.Start.Truncate(time.Second)
			if lower.Before(window.Start) {
				lower = lower.Add(time.Second)
			}
			upper := window.End.Truncate(time.Second)
			contributions = append(contributions, `SELECT signal,namespace,service,resource,first_seen,last_seen FROM read_resource_seconds WHERE signal=`+sqlLiteral(signal)+` AND batch_id IN (`+idsSQL(boundary)+`) AND bucket >= `+timeLiteral(lower)+`::TIMESTAMP_NS AND bucket < `+timeLiteral(upper)+`::TIMESTAMP_NS`)
			if !lower.Equal(window.Start) || !upper.Equal(window.End) {
				selected := []telemetry.BatchMetadata{}
				ids := map[string]bool{}
				for _, id := range boundary {
					ids[id] = true
				}
				for _, b := range batches {
					if ids[b.ID] {
						selected = append(selected, b)
					}
				}
				clock := s.Clock()
				predicate := windowPredicate(clock, window) + ` AND (` + clock + ` < ` + timeLiteral(lower) + ` OR ` + clock + ` >= ` + timeLiteral(upper) + `)`
				contributions = append(contributions, `SELECT `+sqlLiteral(signal)+` AS signal,namespace,service,resource,min(`+clock+`) AS first_seen,max(`+clock+`) AS last_seen FROM (`+cleanSource(signal, d.snapshotSource(signal, selected, batches))+`) WHERE `+predicate+` GROUP BY namespace,service,resource`)
			}
		}

		if len(raw) > 0 {
			clock := s.Clock()
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
