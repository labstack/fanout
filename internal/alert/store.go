package alert

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Store provides CRUD operations for alert rules and alert instances.
type Store struct {
	db *sql.DB
}

// NewStore creates a Store backed by the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// newID returns a new UUIDv7 string.
func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// Fallback to v4 if v7 fails (should not happen in practice).
		return uuid.New().String()
	}
	return id.String()
}

// CreateRule inserts a new alert rule and returns the persisted record.
func (s *Store) CreateRule(r Rule) (Rule, error) {
	r.ID = newID()
	_, err := s.db.Exec(`
		INSERT INTO alert_rules
			(id, name, description, enabled, service, namespace, expression,
			 for_seconds, cooldown_s, repeat_interval_s, webhook_url,
			 webhook_headers, webhook_template, notify_on_resolve)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.Description, boolToInt(r.Enabled),
		r.Service, r.Namespace, r.Expression,
		r.ForSeconds, r.CooldownS, r.RepeatIntervalS,
		r.WebhookURL, r.WebhookHeaders, r.WebhookTemplate,
		boolToInt(r.NotifyOnResolve),
	)
	if err != nil {
		return Rule{}, fmt.Errorf("store: create rule: %w", err)
	}
	return s.GetRule(r.ID)
}

// GetRule retrieves a rule by ID.
func (s *Store) GetRule(id string) (Rule, error) {
	var r Rule
	var description, service, namespace sql.NullString
	var webhookURL, webhookHeaders, webhookTemplate sql.NullString
	var enabled, notifyOnResolve int

	err := s.db.QueryRow(`
		SELECT id, name, description, enabled, service, namespace, expression,
		       for_seconds, cooldown_s, repeat_interval_s, webhook_url,
		       webhook_headers, webhook_template, notify_on_resolve,
		       created_at, updated_at
		FROM alert_rules WHERE id = ?`, id,
	).Scan(
		&r.ID, &r.Name, &description, &enabled,
		&service, &namespace, &r.Expression,
		&r.ForSeconds, &r.CooldownS, &r.RepeatIntervalS,
		&webhookURL, &webhookHeaders, &webhookTemplate,
		&notifyOnResolve, &r.CreatedAt, &r.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return Rule{}, fmt.Errorf("store: rule %q not found", id)
	}
	if err != nil {
		return Rule{}, fmt.Errorf("store: get rule: %w", err)
	}

	r.Description = description.String
	r.Service = service.String
	r.Namespace = namespace.String
	r.WebhookURL = webhookURL.String
	r.WebhookHeaders = webhookHeaders.String
	r.WebhookTemplate = webhookTemplate.String
	r.Enabled = enabled != 0
	r.NotifyOnResolve = notifyOnResolve != 0
	return r, nil
}

// ListRules returns all rules ordered by creation time descending.
func (s *Store) ListRules() ([]Rule, error) {
	return s.queryRules(`SELECT id, name, description, enabled, service, namespace,
		expression, for_seconds, cooldown_s, repeat_interval_s, webhook_url,
		webhook_headers, webhook_template, notify_on_resolve, created_at, updated_at
		FROM alert_rules ORDER BY created_at DESC`, nil)
}

// ListEnabledRules returns only enabled rules.
func (s *Store) ListEnabledRules() ([]Rule, error) {
	return s.queryRules(`SELECT id, name, description, enabled, service, namespace,
		expression, for_seconds, cooldown_s, repeat_interval_s, webhook_url,
		webhook_headers, webhook_template, notify_on_resolve, created_at, updated_at
		FROM alert_rules WHERE enabled = 1 ORDER BY created_at DESC`, nil)
}

func (s *Store) queryRules(query string, args []any) ([]Rule, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query rules: %w", err)
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		var r Rule
		var description, service, namespace sql.NullString
		var webhookURL, webhookHeaders, webhookTemplate sql.NullString
		var enabled, notifyOnResolve int

		if err := rows.Scan(
			&r.ID, &r.Name, &description, &enabled,
			&service, &namespace, &r.Expression,
			&r.ForSeconds, &r.CooldownS, &r.RepeatIntervalS,
			&webhookURL, &webhookHeaders, &webhookTemplate,
			&notifyOnResolve, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan rule: %w", err)
		}

		r.Description = description.String
		r.Service = service.String
		r.Namespace = namespace.String
		r.WebhookURL = webhookURL.String
		r.WebhookHeaders = webhookHeaders.String
		r.WebhookTemplate = webhookTemplate.String
		r.Enabled = enabled != 0
		r.NotifyOnResolve = notifyOnResolve != 0
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// UpdateRule updates a rule by ID and returns the updated record.
func (s *Store) UpdateRule(r Rule) (Rule, error) {
	res, err := s.db.Exec(`
		UPDATE alert_rules SET
			name=?, description=?, enabled=?, service=?, namespace=?,
			expression=?, for_seconds=?, cooldown_s=?, repeat_interval_s=?,
			webhook_url=?, webhook_headers=?, webhook_template=?,
			notify_on_resolve=?, updated_at=datetime('now')
		WHERE id=?`,
		r.Name, r.Description, boolToInt(r.Enabled), r.Service, r.Namespace,
		r.Expression, r.ForSeconds, r.CooldownS, r.RepeatIntervalS,
		r.WebhookURL, r.WebhookHeaders, r.WebhookTemplate,
		boolToInt(r.NotifyOnResolve), r.ID,
	)
	if err != nil {
		return Rule{}, fmt.Errorf("store: update rule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Rule{}, fmt.Errorf("store: rule %q not found", r.ID)
	}
	return s.GetRule(r.ID)
}

// DeleteRule deletes a rule by ID (cascades to alerts).
func (s *Store) DeleteRule(id string) error {
	res, err := s.db.Exec(`DELETE FROM alert_rules WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete rule: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("store: rule %q: %w", id, ErrNotFound)
	}
	return nil
}

// UpsertAlert inserts or updates an alert instance keyed on (rule_id, service).
func (s *Store) UpsertAlert(a Alert) (Alert, error) {
	if a.ID == "" {
		a.ID = newID()
	}
	_, err := s.db.Exec(`
		INSERT INTO alerts
			(id, rule_id, service, state, value, fired_at, resolved_at,
			 repeated_at, last_eval, last_delivery_status, last_delivery_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rule_id, service) DO UPDATE SET
			state=excluded.state,
			value=excluded.value,
			fired_at=excluded.fired_at,
			resolved_at=excluded.resolved_at,
			repeated_at=excluded.repeated_at,
			last_eval=excluded.last_eval,
			last_delivery_status=excluded.last_delivery_status,
			last_delivery_at=excluded.last_delivery_at`,
		a.ID, a.RuleID, a.Service, a.State, a.Value,
		nullString(a.FiredAt), nullString(a.ResolvedAt), nullString(a.RepeatedAt),
		nullString(a.LastEval), nullString(a.LastDeliveryStatus), nullString(a.LastDeliveryAt),
	)
	if err != nil {
		return Alert{}, fmt.Errorf("store: upsert alert: %w", err)
	}
	return s.GetAlert(a.RuleID, a.Service)
}

// GetAlert retrieves an alert by (rule_id, service).
func (s *Store) GetAlert(ruleID, service string) (Alert, error) {
	var a Alert
	var firedAt, resolvedAt, repeatedAt, lastEval sql.NullString
	var lastDeliveryStatus, lastDeliveryAt sql.NullString

	err := s.db.QueryRow(`
		SELECT id, rule_id, service, state, value, fired_at, resolved_at,
		       repeated_at, last_eval, last_delivery_status, last_delivery_at,
		       created_at
		FROM alerts WHERE rule_id=? AND service=?`, ruleID, service,
	).Scan(
		&a.ID, &a.RuleID, &a.Service, &a.State, &a.Value,
		&firedAt, &resolvedAt, &repeatedAt, &lastEval,
		&lastDeliveryStatus, &lastDeliveryAt, &a.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return Alert{}, fmt.Errorf("store: alert (%q, %q): %w", ruleID, service, ErrNotFound)
	}
	if err != nil {
		return Alert{}, fmt.Errorf("store: get alert: %w", err)
	}

	a.FiredAt = firedAt.String
	a.ResolvedAt = resolvedAt.String
	a.RepeatedAt = repeatedAt.String
	a.LastEval = lastEval.String
	a.LastDeliveryStatus = lastDeliveryStatus.String
	a.LastDeliveryAt = lastDeliveryAt.String
	return a, nil
}

// ListAlerts returns alerts with optional filters for state, service, and ruleID.
func (s *Store) ListAlerts(state, service, ruleID string) ([]Alert, error) {
	where := []string{}
	args := []any{}

	if state != "" {
		where = append(where, "state=?")
		args = append(args, state)
	}
	if service != "" {
		where = append(where, "service=?")
		args = append(args, service)
	}
	if ruleID != "" {
		where = append(where, "rule_id=?")
		args = append(args, ruleID)
	}

	q := `SELECT id, rule_id, service, state, value, fired_at, resolved_at,
		repeated_at, last_eval, last_delivery_status, last_delivery_at, created_at
		FROM alerts`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY created_at DESC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list alerts: %w", err)
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		var firedAt, resolvedAt, repeatedAt, lastEval sql.NullString
		var lastDeliveryStatus, lastDeliveryAt sql.NullString

		if err := rows.Scan(
			&a.ID, &a.RuleID, &a.Service, &a.State, &a.Value,
			&firedAt, &resolvedAt, &repeatedAt, &lastEval,
			&lastDeliveryStatus, &lastDeliveryAt, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan alert: %w", err)
		}

		a.FiredAt = firedAt.String
		a.ResolvedAt = resolvedAt.String
		a.RepeatedAt = repeatedAt.String
		a.LastEval = lastEval.String
		a.LastDeliveryStatus = lastDeliveryStatus.String
		a.LastDeliveryAt = lastDeliveryAt.String
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// DeleteAlert deletes an alert by ID.
func (s *Store) DeleteAlert(id string) error {
	_, err := s.db.Exec(`DELETE FROM alerts WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("store: delete alert: %w", err)
	}
	return nil
}

// UpdateDeliveryStatus updates only the delivery status columns for a (rule_id, service) pair.
// This is used by fireWebhookAsync to avoid overwriting alert state that may have changed
// between when the goroutine was launched and when the webhook completed.
func (s *Store) UpdateDeliveryStatus(ruleID, service, status, deliveredAt string) error {
	_, err := s.db.Exec(`
		UPDATE alerts SET last_delivery_status=?, last_delivery_at=?
		WHERE rule_id=? AND service=?`,
		status, deliveredAt, ruleID, service,
	)
	if err != nil {
		return fmt.Errorf("store: update delivery status: %w", err)
	}
	return nil
}

// AlertSummary returns counts of alerts grouped by state.
func (s *Store) AlertSummary() (AlertSummary, error) {
	rows, err := s.db.Query(`SELECT state, COUNT(*) FROM alerts GROUP BY state`)
	if err != nil {
		return AlertSummary{}, fmt.Errorf("store: alert summary: %w", err)
	}
	defer rows.Close()

	var sum AlertSummary
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return AlertSummary{}, fmt.Errorf("store: scan summary: %w", err)
		}
		switch state {
		case "firing":
			sum.Firing = count
		case "pending":
			sum.Pending = count
		case "resolved":
			sum.Resolved = count
		}
	}
	return sum, rows.Err()
}

// PruneResolved deletes resolved alerts older than days days.
// Returns the number of rows deleted.
func (s *Store) PruneResolved(days int) (int64, error) {
	res, err := s.db.Exec(
		`DELETE FROM alerts WHERE state='resolved' AND resolved_at < datetime('now', ? || ' days')`,
		fmt.Sprintf("-%d", days),
	)
	if err != nil {
		return 0, fmt.Errorf("store: prune resolved: %w", err)
	}
	return res.RowsAffected()
}

// boolToInt converts a bool to an integer for SQLite storage.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullString wraps an empty string as sql.NullString{Valid: false}.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
