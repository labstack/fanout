package alert

import (
	"context"
	"errors"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/expr-lang/expr/vm"

	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/query"
)

const pruneHistDays = 7

// Engine drives the alert evaluation loop.
type Engine struct {
	store       *Store
	duck        *query.Duck            // may be nil in tests
	detector    *intelligence.Detector // may be nil in tests
	mu          sync.RWMutex
	programs    map[string]*vm.Program // rule ID → compiled program
	interval    time.Duration
	histDays    int
	envOverride map[string]AlertEnv // for testing — bypasses DuckDB query
}

// NewEngine creates an Engine ready to Run.
func NewEngine(
	store *Store,
	duck *query.Duck,
	detector *intelligence.Detector,
	interval time.Duration,
	histDays int,
) *Engine {
	return &Engine{
		store:    store,
		duck:     duck,
		detector: detector,
		programs: make(map[string]*vm.Program),
		interval: interval,
		histDays: histDays,
	}
}

// Store exposes the underlying store for MCP tools.
func (e *Engine) Store() *Store { return e.store }

// RecompileRule compiles (or recompiles) the program for a single rule.
func (e *Engine) RecompileRule(ruleID, expression string) error {
	prog, err := CompileExpression(expression)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.programs[ruleID] = prog
	return nil
}

// RemoveRule removes a compiled program so it is not evaluated on future ticks.
func (e *Engine) RemoveRule(ruleID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.programs, ruleID)
}

// Run starts the evaluation ticker. It blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	// Evaluate once immediately so alerts fire quickly after startup.
	e.safeEvaluateOnce(ctx)

	for {
		select {
		case <-ticker.C:
			e.safeEvaluateOnce(ctx)
			e.pruneOldAlerts()
		case <-ctx.Done():
			return
		}
	}
}

// BuildEnvForService returns the AlertEnv for a single service (for MCP test action).
func (e *Engine) BuildEnvForService(ctx context.Context, service string) (AlertEnv, bool) {
	envs := e.buildEnvs(ctx)
	if envs == nil {
		return AlertEnv{}, false
	}
	env, ok := envs[service]
	return env, ok
}

// BuildAllEnvs returns all current AlertEnvs (for MCP alert_env tool).
func (e *Engine) BuildAllEnvs(ctx context.Context) map[string]AlertEnv {
	envs := e.buildEnvs(ctx)
	if envs == nil {
		return map[string]AlertEnv{}
	}
	return envs
}

// ---- internal ----

func (e *Engine) safeEvaluateOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("alert: engine panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	e.evaluateOnce(ctx)
}

func (e *Engine) evaluateOnce(ctx context.Context) {
	rules, err := e.store.ListEnabledRules()
	if err != nil {
		slog.Error("alert: list enabled rules", "err", err)
		return
	}
	if len(rules) == 0 {
		return
	}

	e.compileRules(rules)

	envs := e.buildEnvs(ctx)
	if envs == nil {
		return // DuckDB error — skip evaluation entirely, don't false-fire
	}

	for _, rule := range rules {
		e.mu.RLock()
		prog, ok := e.programs[rule.ID]
		e.mu.RUnlock()
		if !ok {
			// Compilation already logged the error; skip.
			continue
		}

		services := e.resolveServices(rule, envs)
		for _, svc := range services {
			env := envs[svc]
			triggered, evalErr := SafeEval(prog, env)
			if evalErr != nil {
				slog.Warn("alert: eval error", "rule", rule.ID, "service", svc, "err", evalErr)
				triggered = false
			}
			e.transition(rule, svc, triggered, env)
		}
	}
}

// compileRules compiles any rules that have not yet been compiled.
func (e *Engine) compileRules(rules []Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range rules {
		if _, ok := e.programs[r.ID]; ok {
			continue
		}
		prog, err := CompileExpression(r.Expression)
		if err != nil {
			slog.Error("alert: compile expression", "rule", r.ID, "err", err)
			continue
		}
		e.programs[r.ID] = prog
	}
}

// resolveServices returns the sorted list of services to evaluate for a rule.
// '*' matches all services present in envs; a specific name targets just that one.
func (e *Engine) resolveServices(rule Rule, envs map[string]AlertEnv) []string {
	if rule.Service == "" || rule.Service == "*" {
		svcs := make([]string, 0, len(envs))
		for svc := range envs {
			svcs = append(svcs, svc)
		}
		sort.Strings(svcs)
		return svcs
	}
	if _, ok := envs[rule.Service]; ok {
		return []string{rule.Service}
	}
	// Service not present in current envs; still evaluate with a zero env so
	// the rule can fire if a static threshold is crossed (e.g. throughput == 0).
	return []string{rule.Service}
}

// transition implements the alert state machine for a (rule, service) pair.
func (e *Engine) transition(rule Rule, svc string, triggered bool, env AlertEnv) {
	now := time.Now()
	nowStr := now.Format(time.RFC3339)

	existing, err := e.store.GetAlert(rule.ID, svc)
	noAlert := errors.Is(err, ErrNotFound)
	if err != nil && !noAlert {
		slog.Error("alert: get alert", "rule", rule.ID, "service", svc, "err", err)
		return
	}

	switch {
	case noAlert && triggered && rule.ForSeconds == 0:
		// Immediately firing.
		a := Alert{
			RuleID:   rule.ID,
			Service:  svc,
			State:    "firing",
			Value:    env.ErrorRate,
			FiredAt:  nowStr,
			LastEval: nowStr,
		}
		saved, upsertErr := e.store.UpsertAlert(a)
		if upsertErr != nil {
			slog.Error("alert: upsert firing", "rule", rule.ID, "service", svc, "err", upsertErr)
			return
		}
		e.fireWebhookAsync(rule, saved, env, "firing")

	case noAlert && triggered && rule.ForSeconds > 0:
		// Enter pending state; wait for the for-duration to elapse.
		a := Alert{
			RuleID:   rule.ID,
			Service:  svc,
			State:    "pending",
			Value:    env.ErrorRate,
			LastEval: nowStr,
		}
		if _, upsertErr := e.store.UpsertAlert(a); upsertErr != nil {
			slog.Error("alert: upsert pending", "rule", rule.ID, "service", svc, "err", upsertErr)
		}

	case noAlert && !triggered:
		// Nothing to do.

	case !noAlert && existing.State == "pending" && triggered:
		// Check whether the for-duration has elapsed.
		createdAt, parseErr := time.Parse(time.RFC3339, existing.CreatedAt)
		if parseErr != nil {
			slog.Error("alert: invalid created_at, skipping", "rule", rule.ID, "service", svc, "created_at", existing.CreatedAt, "err", parseErr)
			return
		}
		forDuration := time.Duration(rule.ForSeconds) * time.Second
		if now.Sub(createdAt) >= forDuration {
			// Promote to firing.
			existing.State = "firing"
			existing.FiredAt = nowStr
			existing.LastEval = nowStr
			saved, upsertErr := e.store.UpsertAlert(existing)
			if upsertErr != nil {
				slog.Error("alert: promote to firing", "rule", rule.ID, "service", svc, "err", upsertErr)
				return
			}
			e.fireWebhookAsync(rule, saved, env, "firing")
		} else {
			// Still waiting.
			existing.LastEval = nowStr
			if _, upsertErr := e.store.UpsertAlert(existing); upsertErr != nil {
				slog.Error("alert: update pending last_eval", "rule", rule.ID, "service", svc, "err", upsertErr)
			}
		}

	case !noAlert && existing.State == "pending" && !triggered:
		// False alarm — delete the pending alert.
		if delErr := e.store.DeleteAlert(existing.ID); delErr != nil {
			slog.Error("alert: delete false-alarm pending", "rule", rule.ID, "service", svc, "err", delErr)
		}

	case !noAlert && existing.State == "firing" && triggered:
		// Already firing; send repeat notification if repeat_interval has elapsed.
		existing.LastEval = nowStr
		existing.Value = env.ErrorRate
		if rule.RepeatIntervalS > 0 {
			ref := existing.RepeatedAt
			if ref == "" {
				ref = existing.FiredAt
			}
			refTime, parseErr := time.Parse(time.RFC3339, ref)
			if parseErr != nil {
				slog.Error("alert: invalid ref timestamp for repeat", "rule", rule.ID, "service", svc, "ref", ref, "err", parseErr)
			}
			repeatInterval := time.Duration(rule.RepeatIntervalS) * time.Second
			if parseErr == nil && now.Sub(refTime) >= repeatInterval {
				existing.RepeatedAt = nowStr
				saved, upsertErr := e.store.UpsertAlert(existing)
				if upsertErr != nil {
					slog.Error("alert: update repeated_at", "rule", rule.ID, "service", svc, "err", upsertErr)
					return
				}
				e.fireWebhookAsync(rule, saved, env, "reminder")
				return
			}
		}
		if _, upsertErr := e.store.UpsertAlert(existing); upsertErr != nil {
			slog.Error("alert: update firing last_eval", "rule", rule.ID, "service", svc, "err", upsertErr)
		}

	case !noAlert && existing.State == "firing" && !triggered:
		// Condition cleared — resolve.
		existing.State = "resolved"
		existing.ResolvedAt = nowStr
		existing.LastEval = nowStr
		saved, upsertErr := e.store.UpsertAlert(existing)
		if upsertErr != nil {
			slog.Error("alert: resolve alert", "rule", rule.ID, "service", svc, "err", upsertErr)
			return
		}
		if rule.NotifyOnResolve {
			e.fireWebhookAsync(rule, saved, env, "resolved")
		}

	case !noAlert && existing.State == "resolved" && triggered:
		// Re-fire from resolved state.
		if rule.ForSeconds == 0 {
			existing.State = "firing"
			existing.FiredAt = nowStr
			existing.ResolvedAt = ""
			existing.LastEval = nowStr
			existing.Value = env.ErrorRate
			saved, upsertErr := e.store.UpsertAlert(existing)
			if upsertErr != nil {
				slog.Error("alert: re-fire from resolved", "rule", rule.ID, "service", svc, "err", upsertErr)
				return
			}
			e.fireWebhookAsync(rule, saved, env, "firing")
		} else {
			// Enter pending again.
			existing.State = "pending"
			existing.FiredAt = ""
			existing.ResolvedAt = ""
			existing.LastEval = nowStr
			existing.Value = env.ErrorRate
			if _, upsertErr := e.store.UpsertAlert(existing); upsertErr != nil {
				slog.Error("alert: re-pend from resolved", "rule", rule.ID, "service", svc, "err", upsertErr)
			}
		}
	}
}

// fireWebhookAsync fires the webhook for an alert in a goroutine and updates delivery status.
func (e *Engine) fireWebhookAsync(rule Rule, alert Alert, env AlertEnv, event string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("alert: webhook goroutine panic", "rule", rule.ID, "service", alert.Service, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		ctx := ActionContext{
			Rule:  rule,
			Alert: alert,
			Env:   env,
			Event: event,
			Time:  time.Now(),
		}
		status, err := FireWebhook(rule, ctx)
		if err != nil {
			slog.Warn("alert: webhook delivery", "rule", rule.ID, "service", alert.Service, "event", event, "err", err)
		}
		now := time.Now().Format(time.RFC3339)
		if updateErr := e.store.UpdateDeliveryStatus(alert.RuleID, alert.Service, status, now); updateErr != nil {
			slog.Error("alert: update delivery status", "rule", rule.ID, "service", alert.Service, "err", updateErr)
		}
	}()
}

// buildEnvs returns AlertEnv keyed by service name. If envOverride is set it
// is returned directly (for tests). Otherwise DuckDB is queried.
func (e *Engine) buildEnvs(ctx context.Context) map[string]AlertEnv {
	if e.envOverride != nil {
		return e.envOverride
	}
	if e.duck == nil {
		return map[string]AlertEnv{}
	}

	const envSQL = `
WITH current AS (
    SELECT service,
           avg(error_rate) as error_rate,
           avg(p50_ms) as p50, avg(p95_ms) as p95,
           sum(spans) as throughput, sum(log_count) as log_count
    FROM service_rollup
    WHERE bucket >= (SELECT max(bucket) FROM service_rollup) - INTERVAL '5 minutes'
    GROUP BY service
),
previous AS (
    SELECT service,
           avg(error_rate) as error_rate, avg(p95_ms) as p95, sum(spans) as throughput
    FROM service_rollup
    WHERE bucket >= (SELECT max(bucket) FROM service_rollup) - INTERVAL '10 minutes'
      AND bucket < (SELECT max(bucket) FROM service_rollup) - INTERVAL '5 minutes'
    GROUP BY service
)
SELECT c.service, c.error_rate, c.p50, c.p95, c.throughput, c.log_count,
    ((c.error_rate - p.error_rate) / NULLIF(p.error_rate, 0)) * 100 as error_rate_delta,
    ((c.p95 - p.p95) / NULLIF(p.p95, 0)) * 100 as p95_delta,
    ((c.throughput - p.throughput) / NULLIF(p.throughput, 0)) * 100 as throughput_delta
FROM current c LEFT JOIN previous p ON c.service = p.service`

	resp := e.duck.ExecuteSQL(ctx, query.SQLRequest{Query: envSQL})
	if resp.Error != "" {
		slog.Error("alert: buildEnvs query failed", "err", resp.Error)
		return nil
	}

	// Collect health score and z-scores from the detector snapshot.
	var healthScore float64
	zScores := map[string]float64{}
	if e.detector != nil {
		if snap := e.detector.LatestSnapshot(); snap != nil {
			healthScore = snap.HealthScore
			for _, a := range snap.Anomalies {
				// Use the max absolute z-score across all anomalies per service.
				if existing, ok := zScores[a.ServiceName]; !ok || abs(a.ZScore) > abs(existing) {
					zScores[a.ServiceName] = a.ZScore
				}
			}
		}
	}

	envs := make(map[string]AlertEnv, len(resp.Results))
	for _, row := range resp.Results {
		svc, _ := row["service"].(string)
		if svc == "" {
			continue
		}
		env := AlertEnv{
			Service:         svc,
			ErrorRate:       toFloat(row["error_rate"]),
			P50:             toFloat(row["p50"]),
			P95:             toFloat(row["p95"]),
			Throughput:      toFloat(row["throughput"]),
			LogCount:        toFloat(row["log_count"]),
			ErrorRateDelta:  toFloat(row["error_rate_delta"]),
			P95Delta:        toFloat(row["p95_delta"]),
			ThroughputDelta: toFloat(row["throughput_delta"]),
			HealthScore:     healthScore,
			ZScore:          zScores[svc],
		}
		envs[svc] = env
	}
	return envs
}

// pruneOldAlerts deletes resolved alerts older than histDays.
func (e *Engine) pruneOldAlerts() {
	days := e.histDays
	if days <= 0 {
		days = pruneHistDays
	}
	n, err := e.store.PruneResolved(days)
	if err != nil {
		slog.Warn("alert: prune resolved", "err", err)
		return
	}
	if n > 0 {
		slog.Info("alert: pruned resolved alerts", "count", n)
	}
}

// toFloat converts an interface{} value to float64. Returns 0 for unknown types.
func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

// abs returns the absolute value of f.
func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

