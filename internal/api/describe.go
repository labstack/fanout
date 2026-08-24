package api

import "sort"

// This file exists so the documentation can be generated from the authorization
// decision itself rather than from a description of it.
//
// classifyRoute is a switch over paths and methods, and roleCapabilities is a
// map. Both are the real thing the middleware consults on every request. A
// generator that parsed either would be reading the shape of the code and
// inferring the rule; the accessors below let it ask the same question the
// server asks and publish the answer.
//
// Nothing here decides anything. If one of these disagrees with the middleware,
// the accessor is wrong, because the middleware is what runs.

// RoutePolicyPublic and friends name the policy kinds in the form the
// documentation uses. The internal constants are an unexported iota, which is
// right for a switch and useless in a table.
const (
	RoutePolicyPublic            = "public"
	RoutePolicyAuthenticated     = "authenticated"
	RoutePolicyCapability        = "capability"
	RoutePolicyServiceCredential = "service-credential"
	RoutePolicyProtocol          = "protocol"
)

// RouteDoc is what a reader needs to know before calling a route: whether it
// needs a credential, and if so which capability that credential must carry.
type RouteDoc struct {
	Method string
	Path   string
	// Policy is one of the RoutePolicy* constants above.
	Policy string
	// Capability is set only when Policy is RoutePolicyCapability or
	// RoutePolicyServiceCredential; empty otherwise.
	Capability string
}

// DescribeRoute reports how the middleware would classify this method and path.
// The second return is false when the pair reaches no case — which for a
// registered route means the method is not permitted on it.
func DescribeRoute(method, path string) (RouteDoc, bool) {
	policy, ok := classifyRoute(method, path)
	if !ok {
		return RouteDoc{}, false
	}

	doc := RouteDoc{Method: method, Path: path, Capability: string(policy.capability)}
	switch policy.kind {
	case routePolicyPublic:
		doc.Policy = RoutePolicyPublic
	case routePolicyAuthenticated:
		doc.Policy = RoutePolicyAuthenticated
	case routePolicyCapability:
		doc.Policy = RoutePolicyCapability
	case routePolicyServiceCredential:
		doc.Policy = RoutePolicyServiceCredential
	case routePolicyProtocol:
		doc.Policy = RoutePolicyProtocol
	default:
		// A new kind added to the switch without a name here would otherwise be
		// published as an empty cell, which reads as "no requirement".
		return RouteDoc{}, false
	}
	return doc, true
}

// RoleCapabilities returns each role's capabilities, sorted, in the string form
// a table wants. The underlying map is a set of empty structs keyed by type,
// which is the right shape for a lookup and the wrong shape for a document.
func RoleCapabilities() map[string][]string {
	out := make(map[string][]string, len(roleCapabilities))
	for role, caps := range roleCapabilities {
		names := make([]string, 0, len(caps))
		for capability := range caps {
			names = append(names, string(capability))
		}
		sort.Strings(names)
		out[string(role)] = names
	}
	return out
}

// AllCapabilities lists every capability the middleware knows, sorted. Derived
// from the role table rather than restated, so a capability that exists but is
// granted to nobody cannot appear here and a new one needs no edit.
func AllCapabilities() []string {
	seen := map[string]struct{}{}
	for _, caps := range roleCapabilities {
		for capability := range caps {
			seen[string(capability)] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
