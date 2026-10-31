package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/query"
)

type busySchema struct{ fakePanels }

func (*busySchema) Schema(context.Context, query.SchemaRequest) (*query.Schema, error) {
	return nil, query.ErrSchemaBusy
}

func TestTelemetrySchemaBusyIs503WithRetryAfter(t *testing.T) {
	rec := servePanels(t, &busySchema{}, http.MethodGet, "/api/telemetry/schema?window=6h", "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" || !strings.Contains(rec.Body.String(), `"code":"schema_busy"`) {
		t.Fatalf("busy schema: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}
