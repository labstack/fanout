package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestEdgeRelationshipScansDoNotDecodeMessaging(t *testing.T) {
	db := openTestDuck(t)
	seedEdgeRollupFixture(t, db)
	var key, raw string
	if err := db.QueryRow("EXPLAIN (FORMAT JSON) CREATE TEMP TABLE edge_pass_buckets AS "+edgePassBucketsNoMessagingSQL, 50, 200).Scan(&key, &raw); err != nil {
		t.Fatal(err)
	}
	var plan any
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	checked := 0
	var visit func(any)
	visit = func(v any) {
		switch n := v.(type) {
		case []any:
			for _, c := range n {
				visit(c)
			}
		case map[string]any:
			extra, _ := n["extra_info"].(map[string]any)
			if strings.Contains(fmt.Sprint(n["name"]), "SCAN") {
				projections := fmt.Sprint(extra["Projections"])
				if strings.Contains(projections, "messaging_") || strings.Contains(projections, "attributes") {
					t.Errorf("empty messaging window reads attributes: %s", projections)
				}
			}
			if name := fmt.Sprint(extra["CTE Name"]); name == "incoming" || name == "recent" {
				children, _ := n["children"].([]any)
				if len(children) == 0 {
					t.Fatal("relationship CTE has no source")
				}
				source, _ := json.Marshal(children[0])
				if strings.Contains(string(source), "messaging_destination") || strings.Contains(string(source), "messaging_system") {
					t.Errorf("%s relationship scan decodes messaging attributes", name)
				}
				checked++
			}
			visit(n["children"])
		}
	}
	visit(plan)
	if checked != 2 {
		t.Fatalf("checked %d relationship CTEs, want 2", checked)
	}
}

func TestEdgePeerLookupSkipsEmptyCandidates(t *testing.T) {
	db := openTestDuck(t)
	seedEdgeRollupFixture(t, db)
	if _, err := db.Exec(createEdgeRollupTable); err != nil {
		t.Fatal(err)
	}
	var lo, hi time.Time
	if err := db.QueryRow(`SELECT min(start_time)::TIMESTAMP_NS,max(start_time)::TIMESTAMP_NS FROM spans`).Scan(&lo, &hi); err != nil {
		t.Fatal(err)
	}
	lo = lo.Truncate(time.Minute)
	hi = hi.Truncate(time.Minute).Add(time.Minute)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TEMP TABLE edge_pass_buckets AS SELECT DISTINCT namespace,date_trunc('minute',start_time::TIMESTAMP_NS)::TIMESTAMPTZ_NS bucket FROM spans`); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareEdgeParents(t.Context(), tx, lo, hi); err != nil {
		t.Fatal(err)
	}
	havePeers, err := prepareEdgePeerCandidates(t.Context(), tx, lo, hi)
	if err != nil || havePeers {
		t.Fatalf("empty peers=%v: %v", havePeers, err)
	}
	var key, raw string
	if err := tx.QueryRow("EXPLAIN (FORMAT JSON) "+withoutEdgeMessaging(edgeRollupNoPeersInsertSQL), lo, hi).Scan(&key, &raw); err != nil {
		t.Fatal(err)
	}
	var plan any
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		t.Fatal(err)
	}
	var visit func(any)
	visit = func(v any) {
		switch n := v.(type) {
		case []any:
			for _, c := range n {
				visit(c)
			}
		case map[string]any:
			extra, _ := n["extra_info"].(map[string]any)
			table := fmt.Sprint(extra["Table"])
			if (table != "<nil>" && !strings.Contains(table, "edge_pass_")) && strings.Contains(fmt.Sprint(extra["Projections"]), "parent_span_id") {
				t.Error("empty peers still scan history for children")
			}
			visit(n["children"])
		}
	}
	visit(plan)
	if _, err := tx.Exec(withoutEdgeMessaging(edgeRollupNoPeersInsertSQL), lo, hi); err != nil {
		t.Fatal(err)
	}
	var peers int
	if err := tx.QueryRow(`SELECT count(*) FROM edge_rollup WHERE edge_type='peer'`).Scan(&peers); err != nil || peers != 0 {
		t.Fatalf("peer edges=%d: %v", peers, err)
	}
}
