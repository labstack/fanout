package dashboard

import (
	"context"

	"github.com/labstack/fanout/internal/auth"
)

// OwnerMetaKey carries the dashboard owner's user ID in MCP request _meta.
//
// Trust model (three-file invariant): io.fanout/owner-id and
// io.fanout/build-origin are client-suppliable metadata. Dashboard tools trust
// these values ONLY on the in-process path with authenticated owner injection
// from internal/agent/tools.go. ProtectMCP (internal/api/oauth.go) guarantees
// OAuth TokenInfo on every HTTP request; remote TokenInfo takes precedence,
// and remote metadata cannot fabricate ownership or conversation provenance.
const OwnerMetaKey = "io.fanout/owner-id"
const BuildOriginMetaKey = "io.fanout/build-origin"
const OAuthScope = auth.MCPScopeDashboardManage

type ownerContextKey struct{}

func WithOwner(ctx context.Context, ownerID string) context.Context {
	return context.WithValue(ctx, ownerContextKey{}, ownerID)
}

func OwnerFromContext(ctx context.Context) string {
	owner, _ := ctx.Value(ownerContextKey{}).(string)
	return owner
}

type BuildOrigin struct {
	ThreadID       string `json:"thread_id"`
	MessageID      string `json:"message_id"`
	RequestExcerpt string `json:"request_excerpt"`
}
type buildOriginContextKey struct{}

func WithBuildOrigin(ctx context.Context, origin BuildOrigin) context.Context {
	return context.WithValue(ctx, buildOriginContextKey{}, origin)
}
func BuildOriginFromContext(ctx context.Context) (BuildOrigin, bool) {
	origin, ok := ctx.Value(buildOriginContextKey{}).(BuildOrigin)
	return origin, ok && origin != (BuildOrigin{})
}
