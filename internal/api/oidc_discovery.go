package api

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	oidcDiscoveryAttemptTimeout = 5 * time.Second
	oidcDiscoveryInitialBackoff = time.Second
	oidcDiscoveryMaxBackoff     = time.Minute
)

// oidcDiscoveryRetryAfter tells a client when a login is next worth trying:
// no later than the longest wait between discovery attempts.
var oidcDiscoveryRetryAfter = strconv.Itoa(int(oidcDiscoveryMaxBackoff.Seconds()))

type oidcDiscoveryTiming struct {
	attempt, initial, max time.Duration
}

var defaultOIDCDiscoveryTiming = oidcDiscoveryTiming{
	attempt: oidcDiscoveryAttemptTimeout,
	initial: oidcDiscoveryInitialBackoff,
	max:     oidcDiscoveryMaxBackoff,
}

// oidcClient is everything built from a discovered provider.
type oidcClient struct {
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
}

// oidcDiscoveryState is the outcome of the latest discovery attempt; client is
// nil until an attempt succeeds.
type oidcDiscoveryState struct {
	client *oidcClient
	at     time.Time
}

// OIDCDiscovery resolves the identity provider's metadata. An unreachable
// provider must not take ingest and existing sessions down with it, so a failed
// startup attempt is retried in the background while the login routes answer 503.
type OIDCDiscovery struct {
	issuer string
	timing oidcDiscoveryTiming
	build  func(*oidc.Provider) *oidcClient
	state  atomic.Pointer[oidcDiscoveryState]
	done   chan struct{}
}

func (d *OIDCDiscovery) attempt(ctx context.Context) error {
	attemptCtx, cancel := context.WithTimeout(ctx, d.timing.attempt)
	defer cancel()
	provider, err := oidc.NewProvider(attemptCtx, d.issuer)
	if err != nil {
		d.state.Store(&oidcDiscoveryState{at: time.Now()})
		return err
	}
	d.state.Store(&oidcDiscoveryState{client: d.build(provider), at: time.Now()})
	return nil
}

// run retries until discovery succeeds or ctx ends. Only process startup can
// reject a configuration, so an issuer mismatch found here is retried and
// logged like any other failure rather than stopping a running instance.
func (d *OIDCDiscovery) run(ctx context.Context) {
	defer close(d.done)
	delay := d.timing.initial
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		err := d.attempt(ctx)
		if err == nil {
			slog.Info("OIDC discovery succeeded", "issuer", d.issuer)
			return
		}
		if ctx.Err() != nil {
			return
		}
		delay = nextOIDCDiscoveryBackoff(delay, d.timing.max)
		slog.Warn("OIDC discovery failed; sign-in stays unavailable", "issuer", d.issuer, "retry_in", delay, "err", err)
		timer.Reset(delay)
	}
}

func nextOIDCDiscoveryBackoff(delay, limit time.Duration) time.Duration {
	return min(2*delay, limit)
}

func (d *OIDCDiscovery) client() *oidcClient {
	return d.state.Load().client
}

// readiness never reports unhealthy: the instance still serves everything but
// new OIDC logins, and removing it from rotation would make the outage total.
// The endpoint is public and discovery errors can carry the provider's response
// body, so the cause stays in the log.
func (d *OIDCDiscovery) readiness() CheckResult {
	state := d.state.Load()
	result := CheckResult{Status: "ok", UpdatedAt: state.at.UTC().Format(time.RFC3339)}
	if state.client == nil {
		result.Status = "degraded"
		result.Detail = "discovery failing; OIDC sign-in returns 503 until a retry succeeds"
	}
	return result
}

// oidcConfigurationInvalid reports a failure that retrying cannot fix: the
// issuer cannot form a request, or the provider answered for a different issuer.
func oidcConfigurationInvalid(err error) bool {
	var mismatch *oidc.IssuerMismatchError
	var malformed *url.Error
	return errors.As(err, &mismatch) || (errors.As(err, &malformed) && malformed.Op == "parse")
}
