// Wire types for GET /api/overview. The internal in-memory result lives
// in service.OverviewResult; this file owns the HTTP wire shape,
// mirroring the MCP pattern in internal/mcp/overview.go. Types are
// package-private because they're only marshaled via c.JSON inside the
// handler — never returned to a caller outside the api package.

package api

import "github.com/labstack/fanout/internal/service"

// alertsStatus is the tri-state for the alerts wrapper. The tagged-enum
// form makes degenerate combinations (e.g. "disabled but available")
// unrepresentable — there are exactly three valid wire states.
type alertsStatus string

const (
	alertsStatusOK          alertsStatus = "ok"
	alertsStatusUnavailable alertsStatus = "unavailable"
	alertsStatusDisabled    alertsStatus = "disabled"
)

// alertsOut is the wire wrapper around the firing-alerts list. Items is
// always non-nil so the JSON serializes as `[]` (not `null`).
type alertsOut struct {
	Status alertsStatus            `json:"status"`
	Items  []service.OverviewAlert `json:"items"`
}

// overviewResponse is the wire shape of GET /api/overview. Constructed by
// toOverviewResponse from a service.OverviewResult + alertsOut. All array
// fields are guaranteed non-nil; Health is a value (not pointer) because
// the UI handler always requests it.
type overviewResponse struct {
	Health                  service.OverviewHealth     `json:"health"`
	Services                []service.OverviewService  `json:"services"`
	Incidents               []service.OverviewIncident `json:"incidents"`
	Activity                service.OverviewActivity   `json:"activity"`
	RecentErrors            []service.RecentError      `json:"recent_errors"`
	RecentErrorsUnavailable bool                       `json:"recent_errors_unavailable"`
	Alerts                  alertsOut                  `json:"alerts"`
}

// toOverviewResponse maps the internal result into the wire shape, normalizing
// nil slices to `[]` and copying Health by value when present (the UI handler
// requests "health", but we don't depend on that here).
func toOverviewResponse(r *service.OverviewResult, alerts alertsOut) overviewResponse {
	out := overviewResponse{
		Services:                r.Services,
		Incidents:               r.Incidents,
		RecentErrors:            r.RecentErrors,
		RecentErrorsUnavailable: r.RecentErrorsUnavailable,
		Alerts:                  alerts,
	}
	if r.Health != nil {
		out.Health = *r.Health
	}
	if r.Activity != nil {
		out.Activity = *r.Activity
	}
	if out.Activity.Buckets == nil {
		out.Activity.Buckets = []service.ActivityBucket{}
	}
	if out.Services == nil {
		out.Services = []service.OverviewService{}
	}
	if out.Incidents == nil {
		out.Incidents = []service.OverviewIncident{}
	}
	if out.RecentErrors == nil {
		out.RecentErrors = []service.RecentError{}
	}
	if out.Alerts.Items == nil {
		out.Alerts.Items = []service.OverviewAlert{}
	}
	return out
}
