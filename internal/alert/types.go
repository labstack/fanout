package alert

import "time"

// Rule defines an alert condition evaluated against the observability data.
type Rule struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	Enabled         bool   `json:"enabled"`
	Service         string `json:"service,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	Expression      string `json:"expression"`
	ForSeconds      int    `json:"for_seconds"`
	CooldownS       int    `json:"cooldown_s"`
	RepeatIntervalS int    `json:"repeat_interval_s"`
	WebhookURL      string `json:"webhook_url,omitempty"`
	WebhookHeaders  string `json:"webhook_headers,omitempty"`
	WebhookTemplate string `json:"webhook_template,omitempty"`
	NotifyOnResolve bool   `json:"notify_on_resolve"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// Alert represents the current state of an alert instance for a (rule, service) pair.
type Alert struct {
	ID                 string  `json:"id"`
	RuleID             string  `json:"rule_id"`
	Service            string  `json:"service"`
	State              string  `json:"state"`
	Value              float64 `json:"value,omitempty"` // Primary metric snapshot (error_rate) at time of evaluation
	FiredAt            string  `json:"fired_at,omitempty"`
	ResolvedAt         string  `json:"resolved_at,omitempty"`
	RepeatedAt         string  `json:"repeated_at,omitempty"`
	LastEval           string  `json:"last_eval,omitempty"`
	LastDeliveryStatus string  `json:"last_delivery_status,omitempty"`
	LastDeliveryAt     string  `json:"last_delivery_at,omitempty"`
	CreatedAt          string  `json:"created_at"`
}

// AlertEnv holds the metric values available to alert expressions.
//
// The `expr` tag is the name a rule author writes, and each field's doc comment
// is published as that variable's description by cmd/fanout-docgen. Adding a
// field here adds a row to the reference; describe it in a sentence a rule
// author would find useful, not in terms of how it is computed.
type AlertEnv struct {
	// Mean of minute-rollup span error proportions over the evaluation window,
	// from 0 to 1; not a request-weighted whole-window error rate.
	ErrorRate float64 `expr:"error_rate"`
	// Mean of approximate minute-rollup median span latencies in milliseconds.
	P50 float64 `expr:"p50"`
	// Mean of approximate minute-rollup 95th-percentile span latencies in
	// milliseconds; not the whole-window p95. Latency prefers served work,
	// falling back to outbound spans when no served work is available.
	P95 float64 `expr:"p95"`
	// Total span count over the current rollup window, not requests per second.
	// Pair it with an error threshold to avoid firing on very little traffic.
	Throughput float64 `expr:"throughput"`
	// Log records recorded for the service over the window.
	LogCount float64 `expr:"log_count"`
	// How far the service's current behaviour sits from its own recent baseline,
	// in standard deviations. Use it to catch a service that has changed without
	// committing to an absolute threshold that fits every service.
	ZScore float64 `expr:"z_score"`
	// The detector's instance-wide composite 0-100 health score. Every service
	// receives the same score; it is not a per-service value.
	HealthScore float64 `expr:"health_score"`
	// Relative change in error rate against the preceding rollup window, as a
	// percentage; zero when the baseline is missing or zero.
	ErrorRateDelta float64 `expr:"error_rate_delta"`
	// Relative change in p95 latency against the preceding rollup window, as a
	// percentage; zero when the baseline is missing or zero.
	P95Delta float64 `expr:"p95_delta"`
	// Relative change in span count against the preceding rollup window, as a
	// percentage. Negative when traffic drops; zero when the baseline is missing
	// or zero.
	ThroughputDelta float64 `expr:"throughput_delta"`
	// The service name being evaluated, as a string. Compare it when one rule
	// needs to behave differently for one service.
	Service string `expr:"service"`
}

// AlertSummary aggregates alert state counts.
type AlertSummary struct {
	Firing   int `json:"firing"`
	Pending  int `json:"pending"`
	Resolved int `json:"resolved"`
}

// ActionContext is passed to webhook templates and delivery logic.
type ActionContext struct {
	Rule  Rule      `json:"rule"`
	Alert Alert     `json:"alert"`
	Env   AlertEnv  `json:"env"`
	Event string    `json:"event"`
	Time  time.Time `json:"time"`
}
