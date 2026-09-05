package auth

import "strings"

const (
	// MCPScopeTelemetryRead grants read access to Fanout observability data.
	MCPScopeTelemetryRead = "telemetry:read"
	// MCPScopeDashboardManage grants access to manage the authenticated user's dashboards.
	MCPScopeDashboardManage = "dashboard:manage"

	legacyMCPScopeRead      = "fanout:read"
	legacyMCPScopeDashboard = "fanout:dashboard"
)

// CanonicalMCPOAuthScope validates an MCP OAuth scope string and returns its
// deduplicated canonical representation. The retired fanout:* names remain
// accepted so existing codes and tokens keep their original authority.
func CanonicalMCPOAuthScope(raw string) (string, bool) {
	var read, dashboards bool
	for _, scope := range strings.Fields(raw) {
		switch scope {
		case MCPScopeTelemetryRead, legacyMCPScopeRead:
			read = true
		case MCPScopeDashboardManage, legacyMCPScopeDashboard:
			dashboards = true
		default:
			return "", false
		}
	}
	if !read {
		return "", false
	}
	if dashboards {
		return MCPScopeTelemetryRead + " " + MCPScopeDashboardManage, true
	}
	return MCPScopeTelemetryRead, true
}

// ResolveMCPRefreshScope applies the optional scope from a refresh request.
// Refreshes may retain or reduce the original grant, but never expand it.
func ResolveMCPRefreshScope(granted, requested string) (string, bool) {
	canonicalGranted, ok := CanonicalMCPOAuthScope(granted)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(requested) == "" {
		return canonicalGranted, true
	}
	canonicalRequested, ok := CanonicalMCPOAuthScope(requested)
	if !ok {
		return "", false
	}
	if canonicalRequested == MCPScopeTelemetryRead+" "+MCPScopeDashboardManage && canonicalGranted != canonicalRequested {
		return "", false
	}
	return canonicalRequested, true
}
