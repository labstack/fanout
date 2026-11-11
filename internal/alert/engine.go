package alert

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/labstack/pitot/internal/engine"
	"github.com/labstack/pitot/internal/intelligence"
)

const pruneHistDays = 7

// Engine drives the alert evaluation loop.
type Engine struct {
	store    *Store
	duck     *engine.Duck           // may be nil in tests
	detector *intelligence.Detector // may be nil in tests
	mu       sync.RWMutex
	programs map[string]Program // rule ID → compiled program
	// stopped holds rules disabled or deleted since a tick last listed them
	// as enabled, so a tick that listed one before it stopped skips it.
	stopped     map[string]bool
	interval    time.Duration
	histDays    int
	lastPrune   time.Time
	envOverride map[string]Env          // for testing — bypasses DuckDB query
	afterList   func()                  // for testing — runs once a tick has listed its rules
	emailHeld   map[string]emailOutcome // outcomes the database refused; worker-owned
	email       *EmailConfig            // nil without SMTP
	now         func() time.Time        // for testing — the email clock
}

// NewEngine creates an Engine ready to Run.
func NewEngine(
	store *Store,
	duck *engine.Duck,
	detector *intelligence.Detector,
	interval time.Duration,
	histDays int,
) *Engine {
	return &Engine{
		store:    store,
		duck:     duck,
		detector: detector,
		programs: make(map[string]Program),
		stopped:  make(map[string]bool),
		interval: interval,
		histDays: histDays,
	}
}

// DisableRule stops evaluating a rule. Its firing alerts resolve, staying in
// history and notifying as a cleared condition would; pending ones never
// fired and are dropped. Holding the lock waits out a tick's transitions for
// the rule, and the tick skips it from then on.
func (e *Engine) DisableRule(rule Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stop(rule.ID)
	alerts, err := e.store.ListAlerts("", "", rule.ID)
	if err != nil {
		slog.Error("alert: list alerts of disabled rule", "rule", rule.ID, "err", err)
		return
	}
	now := time.Now().Format(time.RFC3339)
	for _, a := range alerts {
		switch a.State {
		case "pending":
			if err := e.store.DeleteAlert(a.ID); err != nil {
				slog.Error("alert: drop pending of disabled rule", "rule", rule.ID, "service", a.Service, "err", err)
			}
		case "firing":
			a.State = "resolved"
			a.ResolvedAt = now
			a.LastEvaluatedAt = now
			saved, err := e.saveAlert(rule, a, resolveEvent(rule))
			if err != nil {
				slog.Error("alert: resolve alert of disabled rule", "rule", rule.ID, "service", a.Service, "err", err)
				continue
			}
			if rule.NotifyOnResolve {
				e.fireWebhookAsync(rule, saved, Env{Service: a.Service}, "resolved")
			}
		}
	}
}

// RemoveRule removes a compiled program so it is not evaluated on future ticks.
func (e *Engine) RemoveRule(ruleID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stop(ruleID)
}

// stop drops a rule's program and keeps a tick from compiling it again until
// the rule is recompiled. The caller holds e.mu.
func (e *Engine) stop(ruleID string) {
	delete(e.programs, ruleID)
	e.stopped[ruleID] = true
}

// Run starts the evaluation ticker. It blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	if e.interval <= 0 {
		slog.Error("alert: evaluation interval must be positive", "interval", e.interval)
		return
	}
	if e.email != nil {
		go e.runEmail(ctx)
	} else {
		e.failUndeliverableEmail(ctx)
	}

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
	if e.afterList != nil {
		e.afterList()
	}
	if len(rules) == 0 {
		return
	}

	e.compileRules(rules)

	// Every transition of a tick happens at the tick's start, so holds are
	// measured between ticks however long reading the data takes.
	now := time.Now()
	envs := e.buildEnvs(ctx)
	if envs == nil {
		return // DuckDB error — skip evaluation entirely, don't false-fire
	}

	for _, rule := range rules {
		e.evaluateRule(rule, envs, now)
	}
}

// evaluateRule runs one rule's transitions under the read lock, so a rule
// disabled meanwhile is either skipped or cleaned up after them.
func (e *Engine) evaluateRule(rule Rule, envs map[string]Env, now time.Time) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	prog, ok := e.programs[rule.ID]
	if !ok {
		// Stopped, or compilation already logged the error.
		return
	}
	for _, svc := range e.resolveServices(rule, envs) {
		env := envs[svc]
		triggered, evalErr := SafeEval(prog, env)
		if evalErr != nil {
			slog.Warn("alert: eval error", "rule", rule.ID, "service", svc, "err", evalErr)
			triggered = false
		}
		e.transition(rule, svc, triggered, env, now)
	}
}

// compileRules compiles any listed rules that have not yet been compiled and
// were not stopped since. A stopped rule this tick no longer lists is
// forgotten: later ticks never see it.
func (e *Engine) compileRules(rules []Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	listed := make(map[string]bool, len(rules))
	for _, r := range rules {
		listed[r.ID] = true
	}
	for id := range e.stopped {
		if !listed[id] {
			delete(e.stopped, id)
		}
	}
	for _, r := range rules {
		if _, ok := e.programs[r.ID]; ok || e.stopped[r.ID] {
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
func (e *Engine) resolveServices(rule Rule, envs map[string]Env) []string {
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

// holdSlack absorbs ticker delivery jitter, so a hold that ends on a tick
// fires on that tick.
const holdSlack = time.Second

// transition implements the alert state machine for a (rule, service) pair at
// the evaluation time now. created_at marks when the current pending or firing
// episode began.
func (e *Engine) transition(rule Rule, svc string, triggered bool, env Env, now time.Time) {
	nowStr := now.Format(time.RFC3339)
	began := formatStoredTime(now)

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
			RuleID:          rule.ID,
			Service:         svc,
			State:           "firing",
			Value:           primaryValue(rule, env),
			FiredAt:         nowStr,
			LastEvaluatedAt: nowStr,
			CreatedAt:       began,
		}
		saved, upsertErr := e.saveAlert(rule, a, "firing")
		if upsertErr != nil {
			slog.Error("alert: upsert firing", "rule", rule.ID, "service", svc, "err", upsertErr)
			return
		}
		e.fireWebhookAsync(rule, saved, env, "firing")

	case noAlert && triggered && rule.ForSeconds > 0:
		// Enter pending state; wait for the for-duration to elapse.
		a := Alert{
			RuleID:          rule.ID,
			Service:         svc,
			State:           "pending",
			Value:           primaryValue(rule, env),
			LastEvaluatedAt: nowStr,
			CreatedAt:       began,
		}
		if _, upsertErr := e.store.UpsertAlert(a); upsertErr != nil {
			slog.Error("alert: upsert pending", "rule", rule.ID, "service", svc, "err", upsertErr)
		}

	case noAlert && !triggered:
		// Nothing to do.

	case !noAlert && existing.State == "pending" && triggered:
		// Check whether the for-duration has elapsed.
		createdAt, parseErr := parseStoredTime(existing.CreatedAt)
		if parseErr != nil {
			slog.Error("alert: invalid created_at, skipping", "rule", rule.ID, "service", svc, "created_at", existing.CreatedAt, "err", parseErr)
			return
		}
		forDuration := time.Duration(rule.ForSeconds) * time.Second
		if now.Sub(createdAt)+holdSlack >= forDuration {
			// Promote to firing.
			existing.State = "firing"
			existing.FiredAt = nowStr
			existing.LastEvaluatedAt = nowStr
			saved, upsertErr := e.saveAlert(rule, existing, "firing")
			if upsertErr != nil {
				slog.Error("alert: promote to firing", "rule", rule.ID, "service", svc, "err", upsertErr)
				return
			}
			e.fireWebhookAsync(rule, saved, env, "firing")
		} else {
			// Still waiting.
			existing.LastEvaluatedAt = nowStr
			if _, upsertErr := e.store.UpsertAlert(existing); upsertErr != nil {
				slog.Error("alert: update pending last_evaluated_at", "rule", rule.ID, "service", svc, "err", upsertErr)
			}
		}

	case !noAlert && existing.State == "pending" && !triggered:
		// False alarm — delete the pending alert.
		if delErr := e.store.DeleteAlert(existing.ID); delErr != nil {
			slog.Error("alert: delete false-alarm pending", "rule", rule.ID, "service", svc, "err", delErr)
		}

	case !noAlert && existing.State == "firing" && triggered:
		// Already firing; send repeat notification if repeat_interval has elapsed.
		existing.LastEvaluatedAt = nowStr
		existing.Value = primaryValue(rule, env)
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
				saved, upsertErr := e.saveAlert(rule, existing, "reminder")
				if upsertErr != nil {
					slog.Error("alert: update repeated_at", "rule", rule.ID, "service", svc, "err", upsertErr)
					return
				}
				e.fireWebhookAsync(rule, saved, env, "reminder")
				return
			}
		}
		if _, upsertErr := e.store.UpsertAlert(existing); upsertErr != nil {
			slog.Error("alert: update firing last_evaluated_at", "rule", rule.ID, "service", svc, "err", upsertErr)
		}

	case !noAlert && existing.State == "firing" && !triggered:
		// Condition cleared — resolve.
		existing.State = "resolved"
		existing.ResolvedAt = nowStr
		existing.LastEvaluatedAt = nowStr
		saved, upsertErr := e.saveAlert(rule, existing, resolveEvent(rule))
		if upsertErr != nil {
			slog.Error("alert: resolve alert", "rule", rule.ID, "service", svc, "err", upsertErr)
			return
		}
		if rule.NotifyOnResolve {
			e.fireWebhookAsync(rule, saved, env, "resolved")
		}

	case !noAlert && existing.State == "resolved" && triggered:
		// Re-fire from resolved state — respect cooldown_s if set.
		if rule.CooldownS > 0 && existing.ResolvedAt != "" {
			resolvedAt, parseErr := time.Parse(time.RFC3339, existing.ResolvedAt)
			if parseErr == nil && now.Sub(resolvedAt) < time.Duration(rule.CooldownS)*time.Second {
				// Still in cooldown — update last_evaluated_at but don't re-fire.
				existing.LastEvaluatedAt = nowStr
				if _, upsertErr := e.store.UpsertAlert(existing); upsertErr != nil {
					slog.Error("alert: update cooldown last_evaluated_at", "rule", rule.ID, "service", svc, "err", upsertErr)
				}
				return
			}
		}

		// A new episode gets its own row; the resolved one stays in history.
		a := Alert{
			RuleID:          rule.ID,
			Service:         svc,
			Value:           primaryValue(rule, env),
			LastEvaluatedAt: nowStr,
			CreatedAt:       began,
		}
		if rule.ForSeconds == 0 {
			a.State = "firing"
			a.FiredAt = nowStr
			saved, upsertErr := e.saveAlert(rule, a, "firing")
			if upsertErr != nil {
				slog.Error("alert: re-fire from resolved", "rule", rule.ID, "service", svc, "err", upsertErr)
				return
			}
			e.fireWebhookAsync(rule, saved, env, "firing")
		} else {
			// Enter pending again.
			a.State = "pending"
			if _, upsertErr := e.store.UpsertAlert(a); upsertErr != nil {
				slog.Error("alert: re-pend from resolved", "rule", rule.ID, "service", svc, "err", upsertErr)
			}
		}
	}
}

var (
	quoted   = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
	variable = regexp.MustCompile(`\b(error_rate_delta|p95_delta|throughput_delta|error_rate|p50|p95|throughput|log_count|z_score|health_score)\b`)
)

// primaryValue is the first numeric variable the expression reads, outside
// string literals; the UI names and formats the value by the same rule.
func primaryValue(rule Rule, env Env) float64 {
	name := variable.FindString(quoted.ReplaceAllString(rule.Expression, `""`))
	value, _ := celActivation(env)[name].(float64)
	return value
}

// TestResult is one service's dry run of a rule.
type TestResult struct {
	Service   string `json:"service"`
	Triggered bool   `json:"triggered"`
	Error     string `json:"error,omitempty"`
	// NoData is set when the service has no recent telemetry, which usually
	// means its name is misspelled.
	NoData bool `json:"no_data,omitempty"`
	Env    Env  `json:"env"`
}

var errCurrentData = errors.New("current data could not be read")

// Test evaluates a rule against current data without changing any alert.
func (e *Engine) Test(ctx context.Context, rule Rule) ([]TestResult, error) {
	prog, err := CompileExpression(rule.Expression)
	if err != nil {
		return nil, err
	}
	envs := e.buildEnvs(ctx)
	if envs == nil {
		return nil, errCurrentData
	}
	results := []TestResult{}
	for _, svc := range e.resolveServices(rule, envs) {
		env, seen := envs[svc]
		// A service without data is evaluated with zeros, as the engine does.
		result := TestResult{Service: svc, NoData: !seen, Env: env}
		result.Triggered, err = SafeEval(prog, env)
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
	}
	return results, nil
}

// resolveEvent is the event a resolve notifies, or empty when the rule does not
// notify on resolve.
func resolveEvent(rule Rule) string {
	if rule.NotifyOnResolve {
		return "resolved"
	}
	return ""
}

// fireWebhookAsync fires the webhook for an alert in a goroutine and updates delivery status.
// A rule delivering only by email has no webhook result to record; the email
// worker reports on the alert instead.
func (e *Engine) fireWebhookAsync(rule Rule, alert Alert, env Env, event string) {
	if strings.TrimSpace(rule.WebhookURL) == "" && len(e.emailRecipients(rule)) > 0 {
		return
	}
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
		if updateErr := e.store.UpdateDeliveryStatus(alert.ID, status, now); updateErr != nil {
			slog.Error("alert: update delivery status", "rule", rule.ID, "service", alert.Service, "err", updateErr)
		}
	}()
}

// buildEnvs returns Env keyed by service name. If envOverride is set it
// is returned directly (for tests). Otherwise DuckDB is queried.
func (e *Engine) buildEnvs(ctx context.Context) map[string]Env {
	if e.envOverride != nil {
		return e.envOverride
	}
	if e.duck == nil {
		return map[string]Env{}
	}

	const envSQL = `
WITH current AS (
    SELECT service,
           avg(error_rate) as error_rate,
           COALESCE(avg(p50_ms) FILTER (WHERE served_spans > 0), avg(p50_ms)) as p50,
           COALESCE(avg(p95_ms) FILTER (WHERE served_spans > 0), avg(p95_ms)) as p95,
           sum(spans)::DOUBLE as throughput, sum(log_count)::DOUBLE as log_count
    FROM service_rollup
    WHERE bucket >= (SELECT max(bucket) FROM service_rollup) - INTERVAL '5 minutes'
    GROUP BY service
),
previous AS (
    SELECT service,
           avg(error_rate) as error_rate,
           COALESCE(avg(p95_ms) FILTER (WHERE served_spans > 0), avg(p95_ms)) as p95,
           sum(spans)::DOUBLE as throughput
    FROM service_rollup
    WHERE bucket >= (SELECT max(bucket) FROM service_rollup) - INTERVAL '10 minutes'
      AND bucket < (SELECT max(bucket) FROM service_rollup) - INTERVAL '5 minutes'
    GROUP BY service
)
SELECT c.service, coalesce(c.error_rate, 0), coalesce(c.p50, 0), coalesce(c.p95, 0),
    coalesce(c.throughput, 0), coalesce(c.log_count, 0),
    coalesce(((c.error_rate - p.error_rate) / NULLIF(p.error_rate, 0)) * 100, 0),
    coalesce(((c.p95 - p.p95) / NULLIF(p.p95, 0)) * 100, 0),
    coalesce(((c.throughput - p.throughput) / NULLIF(p.throughput, 0)) * 100, 0)
FROM current c LEFT JOIN previous p ON c.service = p.service
WHERE c.service <> ''`

	// A fixed statement over the rollup cache: the typed read path, not the
	// untrusted-SQL one.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := e.duck.QueryContext(ctx, envSQL)
	if err != nil {
		slog.Error("alert: buildEnvs query failed", "err", err)
		return nil
	}
	defer rows.Close()

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

	envs := map[string]Env{}
	for rows.Next() {
		env := Env{HealthScore: healthScore}
		if err := rows.Scan(&env.Service, &env.ErrorRate, &env.P50, &env.P95, &env.Throughput, &env.LogCount,
			&env.ErrorRateDelta, &env.P95Delta, &env.ThroughputDelta); err != nil {
			slog.Error("alert: buildEnvs scan failed", "err", err)
			return nil
		}
		env.ZScore = zScores[env.Service]
		envs[env.Service] = env
	}
	if err := rows.Err(); err != nil {
		slog.Error("alert: buildEnvs query failed", "err", err)
		return nil
	}
	return envs
}

// pruneOldAlerts deletes resolved alerts older than histDays.
// Runs at most once per hour to avoid unnecessary SQLite churn.
func (e *Engine) pruneOldAlerts() {
	if time.Since(e.lastPrune) < time.Hour {
		return
	}
	days := e.histDays
	if days <= 0 {
		days = pruneHistDays
	}
	n, err := e.store.PruneResolved(days)
	if err != nil {
		slog.Warn("alert: prune resolved", "err", err)
		return
	}
	if err := e.store.pruneEmailDeliveries(days, time.Now()); err != nil {
		slog.Warn("alert: prune email deliveries", "err", err)
		return
	}
	e.lastPrune = time.Now()
	if n > 0 {
		slog.Info("alert: pruned resolved alerts", "count", n)
	}
}

// abs returns the absolute value of f.
func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// RecompileRule compiles an expression and replaces the rule used on future evaluation ticks.
func (e *Engine) RecompileRule(ruleID, expression string) error {
	prog, err := CompileExpression(expression)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.programs[ruleID] = prog
	delete(e.stopped, ruleID)
	return nil
}

// storedTimeLayout is RFC 3339 in UTC with all nine fraction digits, so
// stored episode starts sort as text.
const storedTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// formatStoredTime writes an episode start as the store keeps it.
func formatStoredTime(t time.Time) string {
	return t.UTC().Format(storedTimeLayout)
}

// parseStoredTime reads a time the engine or store wrote (RFC 3339).
func parseStoredTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339, value)
}
