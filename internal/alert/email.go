package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/db/generated"
)

// Rule email statuses, reported on Rule.EmailStatus. Delivery rows use sent,
// failed and suppressed as their terminal states.
const (
	emailSent        = "sent"
	emailRetrying    = "retrying"
	emailFailed      = "failed"
	emailSuppressed  = "suppressed"
	emailUnavailable = "unavailable"

	emailPending = "pending"
	emailSending = "sending"
)

// EmailUnavailableMessage explains why email cannot be chosen or delivered.
const EmailUnavailableMessage = "Email delivery is unavailable because SMTP is not configured. Set FANOUT_SMTP_HOST, FANOUT_SMTP_USERNAME, FANOUT_SMTP_PASSWORD and FANOUT_SMTP_FROM."

const (
	emailBatchSize    = 50
	emailPollInterval = 2 * time.Second
	emailSendTimeout  = 30 * time.Second
	emailLimitWindow  = time.Hour
)

// emailBackoff holds the wait before each attempt. Mail servers defer delivery
// for minutes at a time (greylisting, brief outages), so it runs longer than
// the webhook's in-memory 2s and 4s retries.
var emailBackoff = []time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}

// EmailConfig enables alert email on an Engine.
type EmailConfig struct {
	Send        func(context.Context, auth.AlertMail) error
	Defaults    []string // Addresses for rules with no destination of their own.
	HourlyLimit int      // Emails per address per rolling hour, across all rules.
}

// EmailOptions describes alert email on this instance to API clients.
type EmailOptions struct {
	Available          bool   `json:"available"`
	Message            string `json:"message,omitempty"`
	DefaultDestination bool   `json:"default_destination"`
	MaxRecipients      int    `json:"max_recipients"`
	HourlyLimit        int    `json:"hourly_limit,omitempty"`
}

// EnableEmail turns on alert email. Call it before Run.
func (e *Engine) EnableEmail(cfg EmailConfig) {
	e.email = &cfg
}

// EmailOptions reports whether email can be chosen. A nil engine means alerts
// are disabled.
func (e *Engine) EmailOptions() EmailOptions {
	options := EmailOptions{MaxRecipients: auth.MaxAlertEmailRecipients}
	if e == nil {
		options.Message = "Alert delivery is unavailable because alerts are disabled on this instance."
		return options
	}
	if e.email == nil {
		options.Message = EmailUnavailableMessage
		return options
	}
	options.Available = true
	options.DefaultDestination = len(e.email.Defaults) > 0
	options.HourlyLimit = e.email.HourlyLimit
	return options
}

// WithEmailState adjusts a rule's reported email state for what this instance
// can do now: saved recipients without SMTP are a standing failure, and a rule
// with no email destination has no email state.
func (e *Engine) WithEmailState(rule Rule) Rule {
	if e == nil || e.email == nil {
		rule.EmailStatus, rule.EmailStatusDetail, rule.EmailStatusAt = "", "", ""
		if len(rule.EmailRecipients) > 0 {
			rule.EmailStatus = emailUnavailable
			rule.EmailStatusDetail = e.EmailOptions().Message
		}
		return rule
	}
	if len(e.emailRecipients(rule)) == 0 {
		rule.EmailStatus, rule.EmailStatusDetail, rule.EmailStatusAt = "", "", ""
	}
	return rule
}

// emailRecipients resolves where a rule's notifications are mailed. The default
// destination applies only to a rule with neither a webhook nor recipients.
func (e *Engine) emailRecipients(rule Rule) []string {
	if e.email == nil {
		return nil
	}
	if len(rule.EmailRecipients) > 0 {
		return rule.EmailRecipients
	}
	if strings.TrimSpace(rule.WebhookURL) == "" {
		return e.email.Defaults
	}
	return nil
}

// saveAlert persists a transition. A non-empty event also queues its email in
// the same transaction, so a crash cannot keep one without the other.
func (e *Engine) saveAlert(rule Rule, a Alert, event string) (Alert, error) {
	recipients := e.emailRecipients(rule)
	if event == "" || len(recipients) == 0 {
		return e.store.UpsertAlert(a)
	}
	now := e.clock()
	return e.store.UpsertAlertWithEmail(a, recipients, emailContent{
		Rule:        rule.Name,
		Description: rule.Description,
		Service:     a.Service,
		Event:       event,
		Value:       a.Value,
		Time:        now,
	}, now)
}

func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

// emailContent is what a delivery row keeps to render its message.
type emailContent struct {
	Rule        string    `json:"rule"`
	Description string    `json:"description,omitempty"`
	Service     string    `json:"service"`
	Event       string    `json:"event"`
	Value       float64   `json:"value"`
	Time        time.Time `json:"time"`
}

// UpsertAlertWithEmail saves an alert and queues one delivery per recipient,
// atomically.
func (s *Store) UpsertAlertWithEmail(a Alert, recipients []string, content emailContent, now time.Time) (Alert, error) {
	ctx := context.Background()
	payload, err := json.Marshal(content)
	if err != nil {
		return Alert{}, fmt.Errorf("store: encode email: %w", err)
	}
	notificationID, err := newID()
	if err != nil {
		return Alert{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Alert{}, fmt.Errorf("store: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)
	saved, err := upsertAlert(q, a)
	if err != nil {
		return Alert{}, err
	}
	at := formatStoredTime(now)
	for _, recipient := range recipients {
		id, err := newID()
		if err != nil {
			return Alert{}, err
		}
		if err := q.InsertAlertEmailDelivery(ctx, generated.InsertAlertEmailDeliveryParams{
			ID:             id,
			NotificationID: notificationID,
			RuleID:         saved.RuleID,
			AlertID:        saved.ID,
			Recipient:      recipient,
			Content:        string(payload),
			NextAttemptAt:  at,
			CreatedAt:      at,
		}); err != nil {
			return Alert{}, fmt.Errorf("store: queue email: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Alert{}, fmt.Errorf("store: commit: %w", err)
	}
	return saved, nil
}

func encodeRecipients(recipients []string) sql.NullString {
	if len(recipients) == 0 {
		return sql.NullString{}
	}
	encoded, err := json.Marshal(recipients)
	if err != nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(encoded), Valid: true}
}

func decodeRecipients(ruleID string, stored sql.NullString) []string {
	if !stored.Valid || stored.String == "" {
		return nil
	}
	var recipients []string
	if err := json.Unmarshal([]byte(stored.String), &recipients); err != nil {
		slog.Error("alert: invalid stored email recipients", "rule", ruleID, "err", err)
		return nil
	}
	return recipients
}

// runEmail delivers queued mail until ctx ends. One worker sends sequentially,
// which keeps the per-recipient count exact.
func (e *Engine) runEmail(ctx context.Context) {
	ticker := time.NewTicker(emailPollInterval)
	defer ticker.Stop()
	for {
		e.safeDrainEmail(ctx)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// safeDrainEmail keeps a panic from ending the worker. A row it left marked
// sending is recovered by the next drain.
func (e *Engine) safeDrainEmail(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("alert: email worker panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	e.drainEmail(ctx)
}

// emailOutcome is the bookkeeping for a finished attempt. One that the database
// refuses is held and retried by later drains, so a transient database error
// cannot strand the row or cause mail the server accepted to be sent again.
type emailOutcome struct {
	row   generated.AlertEmailDelivery
	apply func(context.Context) error
	after func(context.Context)
}

// recordOutcome applies an outcome, holding it on failure. It reports success.
func (e *Engine) recordOutcome(ctx context.Context, outcome emailOutcome) bool {
	if err := outcome.apply(ctx); err != nil {
		slog.Error("alert: record email outcome", "rule", outcome.row.RuleID, "err", err)
		if e.emailHeld == nil {
			e.emailHeld = make(map[string]emailOutcome)
		}
		e.emailHeld[outcome.row.ID] = outcome
		return false
	}
	delete(e.emailHeld, outcome.row.ID)
	if outcome.after != nil {
		outcome.after(ctx)
	}
	return true
}

// finishOutcome moves a row to status and then runs after.
func (e *Engine) finishOutcome(row generated.AlertEmailDelivery, status, detail, nextAttemptAt string, after func(context.Context)) emailOutcome {
	return emailOutcome{
		row: row,
		apply: func(ctx context.Context) error {
			return e.finishEmail(ctx, row, status, detail, nextAttemptAt)
		},
		after: after,
	}
}

func (e *Engine) settleAfter(row generated.AlertEmailDelivery) func(context.Context) {
	return func(ctx context.Context) { e.settleEmail(ctx, row) }
}

// recoverEmail handles rows left marked sending by a restart or a panic. Such a
// row may or may not have reached the server, so it is sent again: delivery is
// at least once, and the repeated Message-ID lets a mail system collapse the
// duplicate. It runs only between attempts, so no row is in flight, and rows
// with a held outcome are skipped because their result is already known.
func (e *Engine) recoverEmail(ctx context.Context) {
	rows, err := e.store.q.ListSendingAlertEmailDeliveries(ctx)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("alert: list interrupted email", "err", err)
		}
		return
	}
	now := formatStoredTime(e.clock())
	for _, row := range rows {
		if _, held := e.emailHeld[row.ID]; held {
			continue
		}
		if int(row.Attempts) >= len(emailBackoff) {
			e.recordOutcome(ctx, e.finishOutcome(row, emailFailed, "Interrupted during its last attempt", row.NextAttemptAt, e.settleAfter(row)))
			continue
		}
		e.recordOutcome(ctx, e.finishOutcome(row, emailPending, row.LastError, now, nil))
	}
}

// failUndeliverableEmail settles mail left queued by a run that had SMTP, so it
// shows as failed instead of waiting forever or going out long after the fact.
func (e *Engine) failUndeliverableEmail(ctx context.Context) {
	rows, err := e.store.q.ListOpenAlertEmailDeliveries(ctx)
	if err != nil {
		slog.Error("alert: list open email", "err", err)
		return
	}
	for _, row := range rows {
		e.recordOutcome(ctx, e.finishOutcome(row, emailFailed, EmailUnavailableMessage, row.NextAttemptAt, e.settleAfter(row)))
	}
}

// drainEmail processes everything currently due, oldest first.
func (e *Engine) drainEmail(ctx context.Context) {
	for _, outcome := range e.emailHeld {
		e.recordOutcome(context.WithoutCancel(ctx), outcome)
	}
	e.recoverEmail(ctx)
	for ctx.Err() == nil {
		rows, err := e.store.q.ListDueAlertEmailDeliveries(ctx, generated.ListDueAlertEmailDeliveriesParams{
			NextAttemptAt: formatStoredTime(e.clock()),
			Limit:         emailBatchSize,
		})
		if err != nil {
			if ctx.Err() == nil {
				slog.Error("alert: list due email", "err", err)
			}
			return
		}
		if len(rows) == 0 {
			return
		}
		attempted := false
		for _, row := range rows {
			if _, held := e.emailHeld[row.ID]; held {
				continue
			}
			attempted = true
			if ctx.Err() != nil || !e.deliverEmail(ctx, row) {
				return
			}
		}
		if !attempted || len(rows) < emailBatchSize {
			return
		}
	}
}

// deliverEmail makes one attempt, or suppresses the row if its recipient has
// reached the hourly limit. Suppressed mail is dropped rather than delayed, so
// a burst cannot arrive later as a backlog. It returns false when the database
// failed and the row is still due, so the drain stops until the next poll.
func (e *Engine) deliverEmail(ctx context.Context, row generated.AlertEmailDelivery) bool {
	if row.Attempts > 0 {
		addressed, err := e.stillAddressed(row)
		if err != nil {
			slog.Error("alert: read rule for email retry", "rule", row.RuleID, "err", err)
			return false
		}
		if !addressed {
			if err := e.store.q.DeleteAlertEmailDelivery(ctx, row.ID); err != nil {
				slog.Error("alert: drop email delivery", "rule", row.RuleID, "err", err)
				return false
			}
			e.settleEmail(ctx, row)
			return true
		}
	}

	now := e.clock()
	sent, err := e.store.q.CountAlertEmailSendsSince(ctx, generated.CountAlertEmailSendsSinceParams{
		Recipient: row.Recipient,
		SentAt:    formatStoredTime(now.Add(-emailLimitWindow)),
	})
	if err != nil {
		slog.Error("alert: count sent email", "err", err)
		return false
	}
	limit := e.email.HourlyLimit
	if int(sent) >= limit {
		slog.Warn("alert: email suppressed by hourly limit", "rule", row.RuleID, "alert", row.AlertID, "limit", limit)
		return e.recordOutcome(ctx, e.finishOutcome(row, emailSuppressed, fmt.Sprintf("Over the limit of %d emails per hour to this address", limit), row.NextAttemptAt, e.settleAfter(row)))
	}

	var content emailContent
	if err := json.Unmarshal([]byte(row.Content), &content); err != nil {
		return e.recordOutcome(ctx, e.finishOutcome(row, emailFailed, "The stored message could not be read", row.NextAttemptAt, e.settleAfter(row)))
	}
	claimed, err := e.store.q.ClaimAlertEmailDelivery(ctx, row.ID)
	if err != nil {
		slog.Error("alert: claim email", "err", err)
		return false
	}
	if claimed == 0 {
		return true // Deleted with its rule since the batch was read.
	}
	attempt := int(row.Attempts) + 1

	mail := auth.AlertMail{
		ID:          row.ID,
		To:          row.Recipient,
		Rule:        content.Rule,
		Description: content.Description,
		Service:     content.Service,
		Event:       content.Event,
		Value:       content.Value,
		Time:        content.Time,
	}
	if int(sent) == limit-1 {
		mail.LimitNotice = fmt.Sprintf("This address has reached the limit of %d alert emails per hour. Further alerts are recorded on their rules in Fanout and not emailed until the count falls.", limit)
	}
	sendCtx, cancel := context.WithTimeout(ctx, emailSendTimeout)
	err = e.email.Send(sendCtx, mail)
	cancel()

	detail := ""
	if err != nil {
		detail = auth.MailFailureDetail(err)
	}
	// The outcome is recorded even if shutdown began during the send.
	record := context.WithoutCancel(ctx)
	switch {
	case err == nil:
		sentAt := e.clock()
		return e.recordOutcome(record, emailOutcome{
			row:   row,
			apply: func(c context.Context) error { return e.store.recordEmailSent(c, row, sentAt) },
			after: e.settleAfter(row),
		})
	case ctx.Err() != nil:
		// Shutting down: leave the row marked sending for recoverEmail.
	case auth.IsPermanentMailError(err) || attempt >= len(emailBackoff):
		slog.Warn("alert: email delivery failed", "rule", row.RuleID, "alert", row.AlertID, "attempt", attempt, "reason", detail)
		return e.recordOutcome(record, e.finishOutcome(row, emailFailed, detail, row.NextAttemptAt, e.settleAfter(row)))
	default:
		next := e.clock().Add(emailBackoff[attempt])
		slog.Warn("alert: email attempt failed, will retry", "rule", row.RuleID, "alert", row.AlertID, "attempt", attempt, "reason", detail)
		status := fmt.Sprintf("Attempt %d of %d failed: %s", attempt, len(emailBackoff), detail)
		return e.recordOutcome(record, e.finishOutcome(row, emailPending, detail, formatStoredTime(next), func(c context.Context) {
			e.setRuleEmailStatus(c, row.RuleID, emailRetrying, status)
		}))
	}
	return true
}

// stillAddressed reports whether a retry's recipient is still a destination of
// its rule.
func (e *Engine) stillAddressed(row generated.AlertEmailDelivery) (bool, error) {
	rule, err := e.store.GetRule(row.RuleID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	return slices.Contains(e.emailRecipients(rule), row.Recipient), nil
}

// recordEmailSent marks a row sent and counts it toward the hourly limit,
// atomically.
func (s *Store) recordEmailSent(ctx context.Context, row generated.AlertEmailDelivery, now time.Time) error {
	id, err := newID()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.q.WithTx(tx)
	if err := q.FinishAlertEmailDelivery(ctx, generated.FinishAlertEmailDeliveryParams{
		Status:        emailSent,
		NextAttemptAt: row.NextAttemptAt,
		ID:            row.ID,
	}); err != nil {
		return err
	}
	if err := q.RecordAlertEmailSend(ctx, generated.RecordAlertEmailSendParams{
		ID:        id,
		Recipient: row.Recipient,
		SentAt:    formatStoredTime(now),
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (e *Engine) finishEmail(ctx context.Context, row generated.AlertEmailDelivery, status, detail, nextAttemptAt string) error {
	return e.store.q.FinishAlertEmailDelivery(ctx, generated.FinishAlertEmailDeliveryParams{
		Status:        status,
		LastError:     detail,
		NextAttemptAt: nextAttemptAt,
		ID:            row.ID,
	})
}

// settleEmail publishes a notification's result once every recipient is
// finished: failure outranks suppression, and suppression outranks success. A
// rule without a webhook also reports it on the alert, where the webhook
// result would otherwise go.
func (e *Engine) settleEmail(ctx context.Context, row generated.AlertEmailDelivery) {
	rows, err := e.store.q.ListAlertEmailDeliveriesByNotification(ctx, row.NotificationID)
	if err != nil {
		slog.Error("alert: read notification", "rule", row.RuleID, "err", err)
		return
	}
	if len(rows) == 0 {
		// Every recipient was dropped, or the rule went with them. Only this
		// notification's own retry state is cleared; any other status belongs
		// to a newer notification.
		if err := e.store.q.ClearRetryingRuleEmailStatus(ctx, row.RuleID); err != nil {
			slog.Error("alert: clear rule email status", "rule", row.RuleID, "err", err)
		}
		return
	}
	var failed, suppressed int
	var cause string
	for _, r := range rows {
		switch r.Status {
		case emailPending, emailSending:
			return
		case emailFailed:
			failed++
			if cause == "" {
				cause = r.LastError
			}
		case emailSuppressed:
			suppressed++
			if cause == "" {
				cause = r.LastError
			}
		}
	}

	status, alertStatus, detail := emailSent, "success", ""
	switch {
	case failed > 0:
		status, alertStatus = emailFailed, "failed"
		detail = fmt.Sprintf("%d of %d recipients failed: %s", failed, len(rows), cause)
	case suppressed > 0:
		status, alertStatus = emailSuppressed, "suppressed"
		detail = fmt.Sprintf("%d of %d recipients suppressed: %s", suppressed, len(rows), cause)
	}
	e.setRuleEmailStatus(ctx, row.RuleID, status, detail)

	rule, err := e.store.GetRule(row.RuleID)
	if err != nil {
		slog.Error("alert: read rule for email status", "rule", row.RuleID, "err", err)
		return
	}
	if strings.TrimSpace(rule.WebhookURL) == "" {
		if err := e.store.UpdateDeliveryStatus(row.AlertID, alertStatus, e.clock().Format(time.RFC3339)); err != nil {
			slog.Error("alert: update delivery status", "rule", row.RuleID, "err", err)
		}
	}
}

func (e *Engine) setRuleEmailStatus(ctx context.Context, ruleID, status, detail string) {
	at := ""
	if status != "" {
		at = e.clock().Format(time.RFC3339)
	}
	if err := e.store.q.UpdateRuleEmailStatus(ctx, generated.UpdateRuleEmailStatusParams{
		EmailStatus:       status,
		EmailStatusDetail: detail,
		EmailStatusAt:     at,
		ID:                ruleID,
	}); err != nil {
		slog.Error("alert: update rule email status", "rule", ruleID, "err", err)
	}
}

// pruneEmailDeliveries removes finished rows older than the history window,
// and sends that have aged out of the hourly limit.
func (s *Store) pruneEmailDeliveries(days int, now time.Time) error {
	ctx := context.Background()
	if _, err := s.q.PruneAlertEmailDeliveries(ctx, formatStoredTime(now.AddDate(0, 0, -days))); err != nil {
		return err
	}
	return s.q.PruneAlertEmailSends(ctx, formatStoredTime(now.Add(-emailLimitWindow)))
}
