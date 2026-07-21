package alert

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/labstack/fanout/internal/db/generated"
	appid "github.com/labstack/fanout/internal/id"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// Store provides CRUD operations for alert rules and alert instances.
type Store struct {
	q *generated.Queries
}

// NewStore creates a Store backed by the given database connection.
func NewStore(db *sql.DB) *Store {
	return &Store{q: generated.New(db)}
}

// newID returns a new UUIDv7 string.
func newID() (string, error) {
	id, err := appid.New()
	if err != nil {
		return "", fmt.Errorf("store: generate uuidv7: %w", err)
	}
	return id, nil
}

// ruleToRow converts a domain Rule to generated CreateRuleParams.
func ruleToCreateParams(r Rule) generated.CreateRuleParams {
	return generated.CreateRuleParams{
		ID:              r.ID,
		Name:            r.Name,
		Description:     nullString(r.Description),
		Enabled:         boolToInt64(r.Enabled),
		Service:         nullString(r.Service),
		Namespace:       nullString(r.Namespace),
		Expression:      r.Expression,
		ForSeconds:      int64(r.ForSeconds),
		CooldownS:       int64(r.CooldownS),
		RepeatIntervalS: int64(r.RepeatIntervalS),
		WebhookUrl:      nullString(r.WebhookURL),
		WebhookHeaders:  nullString(r.WebhookHeaders),
		WebhookTemplate: nullString(r.WebhookTemplate),
		NotifyOnResolve: boolToInt64(r.NotifyOnResolve),
	}
}

// ruleToUpdateParams converts a domain Rule to generated UpdateRuleParams.
func ruleToUpdateParams(r Rule) generated.UpdateRuleParams {
	return generated.UpdateRuleParams{
		ID:              r.ID,
		Name:            r.Name,
		Description:     nullString(r.Description),
		Enabled:         boolToInt64(r.Enabled),
		Service:         nullString(r.Service),
		Namespace:       nullString(r.Namespace),
		Expression:      r.Expression,
		ForSeconds:      int64(r.ForSeconds),
		CooldownS:       int64(r.CooldownS),
		RepeatIntervalS: int64(r.RepeatIntervalS),
		WebhookUrl:      nullString(r.WebhookURL),
		WebhookHeaders:  nullString(r.WebhookHeaders),
		WebhookTemplate: nullString(r.WebhookTemplate),
		NotifyOnResolve: boolToInt64(r.NotifyOnResolve),
	}
}

// alertRuleToRule converts a generated.AlertRule to a domain Rule.
func alertRuleToRule(ar generated.AlertRule) Rule {
	return Rule{
		ID:              ar.ID,
		Name:            ar.Name,
		Description:     ar.Description.String,
		Enabled:         ar.Enabled != 0,
		Service:         ar.Service.String,
		Namespace:       ar.Namespace.String,
		Expression:      ar.Expression,
		ForSeconds:      int(ar.ForSeconds),
		CooldownS:       int(ar.CooldownS),
		RepeatIntervalS: int(ar.RepeatIntervalS),
		WebhookURL:      ar.WebhookUrl.String,
		WebhookHeaders:  ar.WebhookHeaders.String,
		WebhookTemplate: ar.WebhookTemplate.String,
		NotifyOnResolve: ar.NotifyOnResolve != 0,
		CreatedAt:       ar.CreatedAt,
		UpdatedAt:       ar.UpdatedAt,
	}
}

// genAlertToAlert converts a generated.Alert to a domain Alert.
func genAlertToAlert(ga generated.Alert) Alert {
	return Alert{
		ID:                 ga.ID,
		RuleID:             ga.RuleID,
		Service:            ga.Service,
		State:              ga.State,
		Value:              ga.Value.Float64,
		FiredAt:            ga.FiredAt.String,
		ResolvedAt:         ga.ResolvedAt.String,
		RepeatedAt:         ga.RepeatedAt.String,
		LastEval:           ga.LastEval.String,
		LastDeliveryStatus: ga.LastDeliveryStatus.String,
		LastDeliveryAt:     ga.LastDeliveryAt.String,
		CreatedAt:          ga.CreatedAt,
	}
}

// alertToUpsertParams converts a domain Alert to generated UpsertAlertParams.
func alertToUpsertParams(a Alert) generated.UpsertAlertParams {
	return generated.UpsertAlertParams{
		ID:      a.ID,
		RuleID:  a.RuleID,
		Service: a.Service,
		State:   a.State,
		Value: sql.NullFloat64{
			Float64: a.Value,
			Valid:   true,
		},
		FiredAt:            nullString(a.FiredAt),
		ResolvedAt:         nullString(a.ResolvedAt),
		RepeatedAt:         nullString(a.RepeatedAt),
		LastEval:           nullString(a.LastEval),
		LastDeliveryStatus: nullString(a.LastDeliveryStatus),
		LastDeliveryAt:     nullString(a.LastDeliveryAt),
	}
}

// CreateRule inserts a new alert rule and returns the persisted record.
func (s *Store) CreateRule(r Rule) (Rule, error) {
	id, err := newID()
	if err != nil {
		return Rule{}, err
	}
	r.ID = id
	ar, err := s.q.CreateRule(context.Background(), ruleToCreateParams(r))
	if err != nil {
		return Rule{}, fmt.Errorf("store: create rule: %w", err)
	}
	return alertRuleToRule(ar), nil
}

// GetRule retrieves a rule by ID.
func (s *Store) GetRule(id string) (Rule, error) {
	ar, err := s.q.GetRule(context.Background(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, fmt.Errorf("store: rule %q not found", id)
	}
	if err != nil {
		return Rule{}, fmt.Errorf("store: get rule: %w", err)
	}
	return alertRuleToRule(ar), nil
}

// ListRules returns all rules ordered by creation time descending.
func (s *Store) ListRules() ([]Rule, error) {
	rows, err := s.q.ListRules(context.Background())
	if err != nil {
		return nil, fmt.Errorf("store: list rules: %w", err)
	}
	rules := make([]Rule, len(rows))
	for i, ar := range rows {
		rules[i] = alertRuleToRule(ar)
	}
	return rules, nil
}

// ListEnabledRules returns only enabled rules.
func (s *Store) ListEnabledRules() ([]Rule, error) {
	rows, err := s.q.ListEnabledRules(context.Background())
	if err != nil {
		return nil, fmt.Errorf("store: list enabled rules: %w", err)
	}
	rules := make([]Rule, len(rows))
	for i, ar := range rows {
		rules[i] = alertRuleToRule(ar)
	}
	return rules, nil
}

// UpdateRule updates a rule by ID and returns the updated record.
func (s *Store) UpdateRule(r Rule) (Rule, error) {
	ar, err := s.q.UpdateRule(context.Background(), ruleToUpdateParams(r))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, fmt.Errorf("store: rule %q not found", r.ID)
	}
	if err != nil {
		return Rule{}, fmt.Errorf("store: update rule: %w", err)
	}
	return alertRuleToRule(ar), nil
}

// DeleteRule deletes a rule by ID (cascades to alerts).
func (s *Store) DeleteRule(id string) error {
	// GetRule first to detect not-found (generated DeleteRule is :exec with no rows info).
	_, err := s.q.GetRule(context.Background(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: rule %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: delete rule (pre-check): %w", err)
	}
	if err := s.q.DeleteRule(context.Background(), id); err != nil {
		return fmt.Errorf("store: delete rule: %w", err)
	}
	return nil
}

// UpsertAlert inserts or updates an alert instance keyed on (rule_id, service).
func (s *Store) UpsertAlert(a Alert) (Alert, error) {
	if a.ID == "" {
		id, err := newID()
		if err != nil {
			return Alert{}, err
		}
		a.ID = id
	}
	ga, err := s.q.UpsertAlert(context.Background(), alertToUpsertParams(a))
	if err != nil {
		return Alert{}, fmt.Errorf("store: upsert alert: %w", err)
	}
	return genAlertToAlert(ga), nil
}

// GetAlert retrieves an alert by (rule_id, service).
func (s *Store) GetAlert(ruleID, service string) (Alert, error) {
	ga, err := s.q.GetAlert(context.Background(), generated.GetAlertParams{
		RuleID:  ruleID,
		Service: service,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Alert{}, fmt.Errorf("store: alert (%q, %q): %w", ruleID, service, ErrNotFound)
	}
	if err != nil {
		return Alert{}, fmt.Errorf("store: get alert: %w", err)
	}
	return genAlertToAlert(ga), nil
}

// ListAlerts returns alerts with optional filters for state, service, and ruleID.
// For a single filter, a dedicated generated query is used. For combined filters
// or no filter, all alerts are fetched and filtered in Go (dataset is small).
func (s *Store) ListAlerts(state, service, ruleID string) ([]Alert, error) {
	ctx := context.Background()

	// Fast paths: single-filter generated queries.
	switch {
	case state != "" && service == "" && ruleID == "":
		rows, err := s.q.ListAlertsByState(ctx, state)
		if err != nil {
			return nil, fmt.Errorf("store: list alerts by state: %w", err)
		}
		return genAlertsToAlerts(rows), nil

	case service != "" && state == "" && ruleID == "":
		rows, err := s.q.ListAlertsByService(ctx, service)
		if err != nil {
			return nil, fmt.Errorf("store: list alerts by service: %w", err)
		}
		return genAlertsToAlerts(rows), nil

	case ruleID != "" && state == "" && service == "":
		rows, err := s.q.ListAlertsByRuleID(ctx, ruleID)
		if err != nil {
			return nil, fmt.Errorf("store: list alerts by rule id: %w", err)
		}
		return genAlertsToAlerts(rows), nil

	default:
		// No filter or combined filters: fetch all and filter in Go.
		rows, err := s.q.ListAlerts(ctx)
		if err != nil {
			return nil, fmt.Errorf("store: list alerts: %w", err)
		}
		all := genAlertsToAlerts(rows)
		if state == "" && service == "" && ruleID == "" {
			return all, nil
		}
		var filtered []Alert
		for _, a := range all {
			if state != "" && a.State != state {
				continue
			}
			if service != "" && a.Service != service {
				continue
			}
			if ruleID != "" && a.RuleID != ruleID {
				continue
			}
			filtered = append(filtered, a)
		}
		return filtered, nil
	}
}

// genAlertsToAlerts converts a slice of generated.Alert to domain []Alert.
func genAlertsToAlerts(rows []generated.Alert) []Alert {
	alerts := make([]Alert, len(rows))
	for i, ga := range rows {
		alerts[i] = genAlertToAlert(ga)
	}
	return alerts
}

// DeleteAlert deletes an alert by ID.
func (s *Store) DeleteAlert(id string) error {
	if err := s.q.DeleteAlert(context.Background(), id); err != nil {
		return fmt.Errorf("store: delete alert: %w", err)
	}
	return nil
}

// UpdateDeliveryStatus updates only the delivery status columns for a (rule_id, service) pair.
// This is used by fireWebhookAsync to avoid overwriting alert state that may have changed
// between when the goroutine was launched and when the webhook completed.
func (s *Store) UpdateDeliveryStatus(ruleID, service, status, deliveredAt string) error {
	err := s.q.UpdateDeliveryStatus(context.Background(), generated.UpdateDeliveryStatusParams{
		LastDeliveryStatus: nullString(status),
		LastDeliveryAt:     nullString(deliveredAt),
		RuleID:             ruleID,
		Service:            service,
	})
	if err != nil {
		return fmt.Errorf("store: update delivery status: %w", err)
	}
	return nil
}

// AlertSummary returns counts of alerts grouped by state.
func (s *Store) AlertSummary() (AlertSummary, error) {
	rows, err := s.q.AlertSummary(context.Background())
	if err != nil {
		return AlertSummary{}, fmt.Errorf("store: alert summary: %w", err)
	}
	var sum AlertSummary
	for _, row := range rows {
		switch row.State {
		case "firing":
			sum.Firing = int(row.Count)
		case "pending":
			sum.Pending = int(row.Count)
		case "resolved":
			sum.Resolved = int(row.Count)
		}
	}
	return sum, nil
}

// PruneResolved deletes resolved alerts older than days days.
// Returns the number of rows deleted.
func (s *Store) PruneResolved(days int) (int64, error) {
	res, err := s.q.PruneResolved(context.Background(), nullString(fmt.Sprintf("-%d", days)))
	if err != nil {
		return 0, fmt.Errorf("store: prune resolved: %w", err)
	}
	return res.RowsAffected()
}

// boolToInt64 converts a bool to int64 for SQLite storage.
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// nullString wraps an empty string as sql.NullString{Valid: false}.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
