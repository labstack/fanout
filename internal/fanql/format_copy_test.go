package fanql

import (
	"testing"

	"github.com/labstack/fanout/internal/query"
)

func TestFormatGaugeExplainsNoGrouping(t *testing.T) {
	query := query.Spec{From: "spans", Measures: []string{"count()"}, By: []string{"service", "operation"}, Bucket: "1m"}
	_, ps, err := Format(t.Context(), nil, query, Options{Viz: "gauge"})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ps {
		if p.Path == "by" && p.Message == "gauge panels take no group by" {
			return
		}
	}
	t.Fatalf("missing grouping explanation: %+v", ps)
}
