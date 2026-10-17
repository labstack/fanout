package api

import (
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	mcpgoauth "github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/time/rate"
)

// serializedMCPOrigin computes the RFC 6454 ASCII serialization once from the
// configured public URL, independent of its path and explicit default port.
func serializedMCPOrigin(publicURL string) string {
	u, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || u.Host == "" {
		return ""
	}
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && !(scheme == "https" && port == "443" || scheme == "http" && port == "80") {
		host += ":" + port
	}
	return scheme + "://" + host
}

// trustedMCPOrigin compares the serialized Origin exactly on public deployments.
func trustedMCPOrigin(r *http.Request, publicOrigin string) bool {
	values, present := r.Header["Origin"]
	if !present {
		return true
	}
	if len(values) != 1 {
		return false
	}
	if publicOrigin != "" {
		return values[0] == publicOrigin
	}
	// With no public origin, accept only same-authority loopback browsers.
	// Keep the SDK's local-server DNS rebinding boundary even behind middleware.
	host := (&url.URL{Host: r.Host}).Hostname()
	ip := net.ParseIP(host)
	return (strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()) &&
		(strings.EqualFold(values[0], "http://"+r.Host) || strings.EqualFold(values[0], "https://"+r.Host))
}

const (
	// Agents usually make a handful of sequential calls per model turn. Twenty
	// calls/second with a burst of 100 leaves ample parallel-query headroom
	// while bounding runaway clients, independently per user and OAuth client.
	mcpToolRate       = rate.Limit(20)
	mcpToolBurst      = 100
	mcpToolMaxEntries = 4096
	mcpToolIdleTTL    = 10 * time.Minute
)

type mcpToolKey struct{ user, client string }
type mcpToolBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}
type mcpToolLimiter struct {
	mu        sync.Mutex
	entries   map[mcpToolKey]*mcpToolBucket
	lastSweep time.Time
	now       func() time.Time
}

func newMCPToolLimiter() *mcpToolLimiter {
	return &mcpToolLimiter{entries: make(map[mcpToolKey]*mcpToolBucket), now: time.Now}
}

func (l *mcpToolLimiter) allow(key mcpToolKey) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) >= time.Minute || len(l.entries) >= mcpToolMaxEntries {
		for key, bucket := range l.entries {
			if now.Sub(bucket.lastSeen) >= mcpToolIdleTTL {
				delete(l.entries, key)
			}
		}
		l.lastSweep = now
	}
	bucket := l.entries[key]
	if bucket == nil {
		if len(l.entries) >= mcpToolMaxEntries {
			return false, int(mcpToolIdleTTL.Seconds())
		}
		bucket = &mcpToolBucket{limiter: rate.NewLimiter(mcpToolRate, mcpToolBurst)}
		l.entries[key] = bucket
	}
	bucket.lastSeen = now
	if bucket.limiter.AllowN(now, 1) {
		return true, 0
	}
	return false, max(1, int(math.Ceil((1-bucket.limiter.TokensAt(now))/float64(mcpToolRate))))
}

func (l *mcpToolLimiter) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := mcpgoauth.TokenInfoFromContext(r.Context())
		if info != nil && r.Method == http.MethodPost && r.Header.Get("Mcp-Method") == "tools/call" {
			client, _ := info.Extra["client_id"].(string)
			if allowed, retry := l.allow(mcpToolKey{info.UserID, client}); !allowed {
				// MCP 2026-07-28 server/tools#security-considerations requires
				// servers to rate limit invocations. No rate-limit RPC code is
				// assigned in basic#error-codes; use HTTP 429 (RFC 6585 §4).
				w.Header().Set("Retry-After", strconv.Itoa(retry))
				http.Error(w, "MCP tool invocation rate exceeded", http.StatusTooManyRequests)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
