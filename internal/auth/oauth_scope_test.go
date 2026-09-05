package auth

import "testing"

func TestCanonicalMCPOAuthScope(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "read", raw: MCPScopeTelemetryRead, want: MCPScopeTelemetryRead, ok: true},
		{name: "dashboard", raw: MCPScopeTelemetryRead + " " + MCPScopeDashboardManage, want: MCPScopeTelemetryRead + " " + MCPScopeDashboardManage, ok: true},
		{name: "legacy", raw: legacyMCPScopeDashboard + " " + legacyMCPScopeRead, want: MCPScopeTelemetryRead + " " + MCPScopeDashboardManage, ok: true},
		{name: "mixed and duplicated", raw: MCPScopeDashboardManage + " " + legacyMCPScopeRead + " " + MCPScopeDashboardManage, want: MCPScopeTelemetryRead + " " + MCPScopeDashboardManage, ok: true},
		{name: "missing read", raw: MCPScopeDashboardManage, ok: false},
		{name: "empty", raw: "", ok: false},
		{name: "unknown", raw: MCPScopeTelemetryRead + " users:manage", ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := CanonicalMCPOAuthScope(test.raw)
			if ok != test.ok || got != test.want {
				t.Fatalf("CanonicalMCPOAuthScope(%q) = %q, %v; want %q, %v", test.raw, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestResolveMCPRefreshScope(t *testing.T) {
	full := MCPScopeTelemetryRead + " " + MCPScopeDashboardManage
	tests := []struct {
		name      string
		granted   string
		requested string
		want      string
		ok        bool
	}{
		{name: "omitted retains grant", granted: full, want: full, ok: true},
		{name: "same grant", granted: full, requested: full, want: full, ok: true},
		{name: "narrowed to read", granted: full, requested: MCPScopeTelemetryRead, want: MCPScopeTelemetryRead, ok: true},
		{name: "legacy names canonicalized", granted: legacyMCPScopeRead + " " + legacyMCPScopeDashboard, requested: legacyMCPScopeRead, want: MCPScopeTelemetryRead, ok: true},
		{name: "cannot expand", granted: MCPScopeTelemetryRead, requested: full, ok: false},
		{name: "unknown requested scope", granted: full, requested: "users:manage", ok: false},
		{name: "invalid stored grant", granted: "users:manage", requested: MCPScopeTelemetryRead, ok: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ResolveMCPRefreshScope(test.granted, test.requested)
			if ok != test.ok || got != test.want {
				t.Fatalf("ResolveMCPRefreshScope(%q, %q) = %q, %v; want %q, %v", test.granted, test.requested, got, ok, test.want, test.ok)
			}
		})
	}
}
