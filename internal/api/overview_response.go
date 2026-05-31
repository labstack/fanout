// Package api — overview_response.go
//
// Wire types for GET /api/overview. The internal in-memory result lives in
// internal/service.OverviewResult; this file owns the HTTP wire shape,
// mirroring the MCP pattern in internal/mcp/overview.go.

package api

import "github.com/labstack/fanout/internal/service"

// AlertsStatus is the tri-state for the alerts wrapper. The tagged-enum
// form makes degenerate combinations (e.g. "disabled but available")
// unrepresentable — there are exactly three valid wire states.
type AlertsStatus string

const (
	AlertsStatusOK          AlertsStatus = "ok"
	AlertsStatusUnavailable AlertsStatus = "unavailable"
	AlertsStatusDisabled    AlertsStatus = "disabled"
)

// AlertsOut is the wire wrapper around the firing-alerts list. Items is
// always non-nil so the JSON serializes as `[]` (not `null`).
type AlertsOut struct {
	Status AlertsStatus            `json:"status"`
	Items  []service.OverviewAlert `json:"items"`
}

// OverviewResponse is the wire shape of GET /api/overview. Constructed by
// toOverviewResponse from a service.OverviewResult + AlertsOut. All array
// fields are guaranteed non-nil; Health is a value (not pointer) because
// the UI handler always requests it.
type OverviewResponse struct {
	Health    service.OverviewHealth     `json:"health"`
	Services  []service.OverviewService  `json:"services"`
	Incidents []service.OverviewIncident `json:"incidents"`
	Alerts    AlertsOut                  `json:"alerts"`
}

// toOverviewResponse maps the internal value type into the wire shape.
// Nil input slices become non-nil empty slices so the JSON serializes as
// `[]` (not `null`). Health is dereferenced (handler guarantees non-nil
// for UI calls — see ui.go Overview which always requests "health").
func toOverviewResponse(r *service.OverviewResult, alerts AlertsOut) OverviewResponse {
	out := OverviewResponse{
		Services:  r.Services,
		Incidents: r.Incidents,
		Alerts:    alerts,
	}
	if r.Health != nil {
		out.Health = *r.Health
	}
	if out.Services == nil {
		out.Services = []service.OverviewService{}
	}
	if out.Incidents == nil {
		out.Incidents = []service.OverviewIncident{}
	}
	if out.Alerts.Items == nil {
		out.Alerts.Items = []service.OverviewAlert{}
	}
	return out
}
