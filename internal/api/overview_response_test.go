package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/service"
)

func TestToOverviewResponse_NilSlicesBecomeEmptyArrays(t *testing.T) {
	// Service layer may return nil slices when sections are empty. The
	// mapper must convert them to non-nil empty slices so the JSON wire
	// shape is `[]` (not `null`).
	in := &service.OverviewResult{
		Health: &service.OverviewHealth{Score: 1.0, TotalServices: 0},
		// Services, Incidents intentionally nil
	}
	alerts := alertsOut{Status: alertsStatusDisabled, Items: nil}

	out := toOverviewResponse(in, alerts)
	if out.Services == nil {
		t.Error("Services should be non-nil empty slice, got nil")
	}
	if out.Incidents == nil {
		t.Error("Incidents should be non-nil empty slice, got nil")
	}
	if out.Alerts.Items == nil {
		t.Error("Alerts.Items should be non-nil empty slice, got nil")
	}

	// Round-trip via JSON to confirm the wire shape.
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"services":[]`,
		`"incidents":[]`,
		`"alerts":{"status":"disabled","items":[]}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("expected %s in JSON, got: %s", want, b)
		}
	}
}

func TestToOverviewResponse_StatusVariants(t *testing.T) {
	tests := []struct {
		name       string
		alerts     alertsOut
		wantInJSON string
	}{
		{
			name:       "ok with one firing",
			alerts:     alertsOut{Status: alertsStatusOK, Items: []service.OverviewAlert{{Rule: "r1", Service: "cart", State: "firing", FiredAt: time.Unix(0, 0).UTC().Format(time.RFC3339)}}},
			wantInJSON: `"alerts":{"status":"ok","items":[{`,
		},
		{
			name:       "unavailable empty",
			alerts:     alertsOut{Status: alertsStatusUnavailable, Items: []service.OverviewAlert{}},
			wantInJSON: `"alerts":{"status":"unavailable","items":[]}`,
		},
		{
			name:       "disabled empty",
			alerts:     alertsOut{Status: alertsStatusDisabled, Items: []service.OverviewAlert{}},
			wantInJSON: `"alerts":{"status":"disabled","items":[]}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := toOverviewResponse(&service.OverviewResult{Health: &service.OverviewHealth{}}, tc.alerts)
			b, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(b), tc.wantInJSON) {
				t.Errorf("expected %s in JSON, got: %s", tc.wantInJSON, b)
			}
		})
	}
}

func TestToOverviewResponse_HealthCopiedByValue(t *testing.T) {
	in := &service.OverviewResult{
		Health: &service.OverviewHealth{Score: 0.42, TotalServices: 3},
	}
	out := toOverviewResponse(in, alertsOut{Status: alertsStatusOK, Items: []service.OverviewAlert{}})
	if out.Health.Score != 0.42 || out.Health.TotalServices != 3 {
		t.Errorf("Health not copied, got %+v", out.Health)
	}
}
