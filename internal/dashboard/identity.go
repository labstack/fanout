package dashboard

import "context"

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
