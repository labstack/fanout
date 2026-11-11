package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/pitot/internal/query"
)

type busySchema struct{ fakePanels }

func (*busySchema) Schema(context.Context, query.SchemaRequest) (*query.Schema, error) {
	return nil, query.ErrSchemaBusy
}

func TestTelemetrySchemaBusyIs503WithRetryAfter(t *testing.T) {
	rec := servePanels(t, &busySchema{}, http.MethodGet, "/api/telemetry/schema?window=6h", "")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" || !strings.Contains(rec.Body.String(), `"code":"schema_busy"`) || strings.Contains(rec.Body.String(), "Panels") {
		t.Fatalf("busy schema: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
}

type slowSchema struct{ fakePanels }

func (*slowSchema) Schema(context.Context, query.SchemaRequest) (*query.Schema, error) {
	return nil, context.DeadlineExceeded
}

// A discovery that overran its own deadline is a schema timeout, not a
// message about panels.
func TestTelemetrySchemaTimeoutHasItsOwnMessage(t *testing.T) {
	rec := servePanels(t, &slowSchema{}, http.MethodGet, "/api/telemetry/schema?window=24h", "")
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), "schema took too long") || strings.Contains(rec.Body.String(), "Panels") {
		t.Fatalf("slow schema: %d %s", rec.Code, rec.Body)
	}
}

type windowSchema struct{ fakePanels }

func (*windowSchema) Schema(context.Context, query.SchemaRequest) (*query.Schema, error) {
	return nil, query.Problems{{Path: "window", Message: "window must be one of 5m, 15m, 1h, 3h, 6h, 12h or 24h"}}
}

func TestTelemetrySchemaUnknownWindowNamesTheWindows(t *testing.T) {
	rec := servePanels(t, &windowSchema{}, http.MethodGet, "/api/telemetry/schema?window=7d", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"bad_request"`) || !strings.Contains(rec.Body.String(), `"message":"window must be one of 5m, 15m, 1h, 3h, 6h, 12h or 24h"`) || strings.Contains(rec.Body.String(), "dashboard") {
		t.Fatalf("unknown window: %d %s", rec.Code, rec.Body)
	}
}

type emptyProblemsSchema struct{ fakePanels }

func (*emptyProblemsSchema) Schema(context.Context, query.SchemaRequest) (*query.Schema, error) {
	return nil, query.Problems{}
}

// An empty problem list is still a bad request, not a panic.
func TestTelemetrySchemaEmptyProblems(t *testing.T) {
	rec := servePanels(t, &emptyProblemsSchema{}, http.MethodGet, "/api/telemetry/schema?window=1h", "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"bad_request"`) {
		t.Fatalf("empty problems: %d %s", rec.Code, rec.Body)
	}
}
