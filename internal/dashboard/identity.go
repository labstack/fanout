package dashboard

import "context"

// OwnerMetaKey carries the dashboard owner's user ID in MCP request _meta.
//
// Trust model (three-file invariant): this key is client-suppliable, and MCP
// dashboard tools (internal/mcp/dashboards.go, dashboardOwner) trust it as
// the owner identity ONLY when the request carries no OAuth TokenInfo. That
// is safe solely because ProtectMCP (internal/api/oauth.go) guarantees
// TokenInfo on every HTTP-transport request — token identity always wins —
// leaving the _meta fallback reachable only via the in-process transport,
// where internal/agent/tools.go injects the already-authenticated user.
const OwnerMetaKey = "io.fanout/owner-id"
const OAuthScope = "fanout:dashboard"

type ownerContextKey struct{}

func WithOwner(ctx context.Context, ownerID string) context.Context {
	return context.WithValue(ctx, ownerContextKey{}, ownerID)
}

func OwnerFromContext(ctx context.Context) string {
	owner, _ := ctx.Value(ownerContextKey{}).(string)
	return owner
}
