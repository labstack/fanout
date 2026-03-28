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
	Value              float64 `json:"value,omitempty"`
	FiredAt            string  `json:"fired_at,omitempty"`
	ResolvedAt         string  `json:"resolved_at,omitempty"`
	RepeatedAt         string  `json:"repeated_at,omitempty"`
	LastEval           string  `json:"last_eval,omitempty"`
	LastDeliveryStatus string  `json:"last_delivery_status,omitempty"`
	LastDeliveryAt     string  `json:"last_delivery_at,omitempty"`
	CreatedAt          string  `json:"created_at"`
}

// AlertEnv holds the metric values available to alert expressions.
type AlertEnv struct {
	ErrorRate       float64 `expr:"error_rate"`
	P50             float64 `expr:"p50"`
	P95             float64 `expr:"p95"`
	P99             float64 `expr:"p99"`
	Throughput      float64 `expr:"throughput"`
	LogCount        float64 `expr:"log_count"`
	ZScore          float64 `expr:"z_score"`
	HealthScore     float64 `expr:"health_score"`
	ErrorRateDelta  float64 `expr:"error_rate_delta"`
	P95Delta        float64 `expr:"p95_delta"`
	ThroughputDelta float64 `expr:"throughput_delta"`
	Service         string  `expr:"service"`
	Namespace       string  `expr:"namespace"`
}

// AlertSummary aggregates alert state counts.
type AlertSummary struct {
	Firing   int `json:"firing"`
	Pending  int `json:"pending"`
	Resolved int `json:"resolved"`
}

// ActionContext is passed to webhook templates and delivery logic.
type ActionContext struct {
	Rule  Rule        `json:"rule"`
	Alert Alert       `json:"alert"`
	Env   AlertEnv    `json:"env"`
	Event string      `json:"event"`
	Time  time.Time   `json:"time"`
}
