package alert

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mail "github.com/wneessen/go-mail"

	"github.com/labstack/fanout/internal/auth"
)

// outbox records sends and fails them on demand.
type outbox struct {
	mu    sync.Mutex
	mails []auth.AlertMail
	fail  func(auth.AlertMail) error
}

func (o *outbox) send(_ context.Context, m auth.AlertMail) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		if err := o.fail(m); err != nil {
			return err
		}
	}
	o.mails = append(o.mails, m)
	return nil
}

func (o *outbox) to(address string) []auth.AlertMail {
	o.mu.Lock()
	defer o.mu.Unlock()
	var matched []auth.AlertMail
	for _, m := range o.mails {
		if m.To == address {
			matched = append(matched, m)
		}
	}
	return matched
}

type mailHarness struct {
	engine *Engine
	store  *Store
	box    *outbox
	now    time.Time
}

func newMailHarness(t *testing.T, limit int, defaults ...string) *mailHarness {
	t.Helper()
	engine, store := newTestEngine(t)
	h := &mailHarness{engine: engine, store: store, box: &outbox{}, now: time.Now().UTC()}
	engine.now = func() time.Time { return h.now }
	engine.EnableEmail(EmailConfig{Send: h.box.send, Defaults: defaults, HourlyLimit: limit})
	return h
}

func (h *mailHarness) rule(t *testing.T, name string, recipients ...string) Rule {
	t.Helper()
	rule, err := h.store.CreateRule(Rule{Name: name, Enabled: true, Service: "api", Expression: "error_rate > 0.1", RepeatIntervalS: 3600, EmailRecipients: recipients})
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func (h *mailHarness) fire(rule Rule) {
	h.engine.transition(rule, "api", true, Env{Service: "api", ErrorRate: 0.5}, h.now)
}

func (h *mailHarness) drain() { h.engine.drainEmail(context.Background()) }

func (h *mailHarness) ruleStatus(t *testing.T, id string) Rule {
	t.Helper()
	rule, err := h.store.GetRule(id)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func TestEmailReachesEveryRecipientAndReportsSent(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com", "b@example.com")
	h.fire(rule)
	h.drain()
	if len(h.box.to("a@example.com")) != 1 || len(h.box.to("b@example.com")) != 1 {
		t.Fatalf("mails = %+v", h.box.mails)
	}
	got := h.box.to("a@example.com")[0]
	if got.Rule != "errors" || got.Service != "api" || got.Event != "firing" || got.Value != 0.5 {
		t.Fatalf("mail = %+v", got)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "sent" {
		t.Fatalf("rule status = %q", status)
	}
	alert, err := h.store.GetAlert(rule.ID, "api")
	if err != nil || alert.LastDeliveryStatus != "success" {
		t.Fatalf("alert = %+v, %v", alert, err)
	}
}

func TestDefaultDestinationAppliesOnlyToRulesWithoutOne(t *testing.T) {
	h := newMailHarness(t, 30, "oncall@example.com")
	bare := h.rule(t, "bare")
	own := h.rule(t, "own", "owner@example.com")
	webhook, err := h.store.CreateRule(Rule{Name: "hook", Enabled: true, Service: "api", Expression: "true", WebhookURL: "http://127.0.0.1:1/hook"})
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []Rule{bare, own, webhook} {
		h.fire(rule)
	}
	h.drain()
	if got := h.box.to("oncall@example.com"); len(got) != 1 || got[0].Rule != "bare" {
		t.Fatalf("default destination got %+v", got)
	}
	if got := h.box.to("owner@example.com"); len(got) != 1 {
		t.Fatalf("own recipient got %+v", got)
	}
}

// A rule row from before this feature has NULL recipients and must still deliver.
func TestOlderRuleWithoutEmailColumnsUsesDefaultDestination(t *testing.T) {
	h := newMailHarness(t, 30, "oncall@example.com")
	if _, err := h.store.db.Exec(`INSERT INTO alert_rules (id, name, expression, for_seconds) VALUES ('old', 'old rule', 'true', 0)`); err != nil {
		t.Fatal(err)
	}
	rule := h.ruleStatus(t, "old")
	if rule.EmailRecipients != nil || rule.EmailStatus != "" {
		t.Fatalf("older rule = %+v", rule)
	}
	h.engine.transition(rule, "api", true, Env{Service: "api"}, h.now)
	h.drain()
	if got := h.box.to("oncall@example.com"); len(got) != 1 {
		t.Fatalf("older rule mails = %+v", got)
	}
}

func TestEmailRetriesWithBackoffWithoutResendingToDelivered(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "ok@example.com", "flaky@example.com")
	failing := true
	h.box.fail = func(m auth.AlertMail) error {
		if m.To == "flaky@example.com" && failing {
			return errors.New("451 try later")
		}
		return nil
	}
	h.fire(rule)
	h.drain()
	stored := h.ruleStatus(t, rule.ID)
	if stored.EmailStatus != "retrying" || !strings.Contains(stored.EmailStatusDetail, "Attempt 1 of 5") || strings.Contains(stored.EmailStatusDetail, "try later") {
		t.Fatalf("rule = %+v", stored)
	}

	h.now = h.now.Add(30 * time.Second)
	h.drain()
	if len(h.box.to("flaky@example.com")) != 0 {
		t.Fatal("retried before the backoff elapsed")
	}

	failing = false
	h.now = h.now.Add(31 * time.Second)
	h.drain()
	if len(h.box.to("flaky@example.com")) != 1 || len(h.box.to("ok@example.com")) != 1 {
		t.Fatalf("mails = %+v", h.box.mails)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "sent" {
		t.Fatalf("rule status = %q", status)
	}
}

func TestExhaustedRetriesShowFailureOnRule(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	attempts := 0
	h.box.fail = func(auth.AlertMail) error {
		attempts++
		return errors.New("connection refused")
	}
	h.fire(rule)
	for range len(emailBackoff) + 2 {
		h.drain()
		h.now = h.now.Add(time.Hour)
	}
	if attempts != len(emailBackoff) {
		t.Fatalf("attempts = %d, want %d", attempts, len(emailBackoff))
	}
	stored := h.ruleStatus(t, rule.ID)
	if stored.EmailStatus != "failed" || stored.EmailStatusDetail == "" || strings.Contains(stored.EmailStatusDetail, "connection refused") {
		t.Fatalf("rule = %+v", stored)
	}
	alert, err := h.store.GetAlert(rule.ID, "api")
	if err != nil || alert.LastDeliveryStatus != "failed" {
		t.Fatalf("alert = %+v, %v", alert, err)
	}
}

func TestPermanentRejectionIsNotRetried(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "gone@example.com")
	attempts := 0
	h.box.fail = func(auth.AlertMail) error {
		attempts++
		return fmt.Errorf("%w: rejected", auth.ErrInvalidMailAddress)
	}
	h.fire(rule)
	h.drain()
	h.now = h.now.Add(time.Hour)
	h.drain()
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "failed" {
		t.Fatalf("rule status = %q", status)
	}
}

func TestBurstAcrossRulesIsBoundedPerRecipientAndSuppressionIsVisible(t *testing.T) {
	h := newMailHarness(t, 3)
	var rules []Rule
	for i := range 20 {
		rules = append(rules, h.rule(t, fmt.Sprintf("rule-%d", i), "pager@example.com"))
	}
	quiet := h.rule(t, "quiet", "other@example.com")
	for _, rule := range rules {
		h.fire(rule)
		h.now = h.now.Add(time.Millisecond)
	}
	h.fire(quiet)
	h.now = h.now.Add(time.Millisecond)
	h.drain()

	delivered := h.box.to("pager@example.com")
	if len(delivered) != 3 {
		t.Fatalf("delivered = %d, want 3", len(delivered))
	}
	if delivered[1].LimitNotice != "" || !strings.Contains(delivered[2].LimitNotice, "limit of 3") {
		t.Fatalf("limit notices = %q, %q", delivered[1].LimitNotice, delivered[2].LimitNotice)
	}
	if len(h.box.to("other@example.com")) != 1 {
		t.Fatal("another recipient was affected by the limit")
	}
	suppressed := 0
	for _, rule := range rules {
		stored := h.ruleStatus(t, rule.ID)
		if stored.EmailStatus == "suppressed" {
			suppressed++
			if !strings.Contains(stored.EmailStatusDetail, "limit of 3") {
				t.Fatalf("detail = %q", stored.EmailStatusDetail)
			}
		}
	}
	if suppressed != 17 {
		t.Fatalf("suppressed rules = %d, want 17", suppressed)
	}

	// Suppressed mail never arrives later, and the window reopens.
	h.now = h.now.Add(2 * time.Hour)
	h.drain()
	if got := len(h.box.to("pager@example.com")); got != 3 {
		t.Fatalf("suppressed mail delivered later: %d", got)
	}
	late := h.rule(t, "late", "pager@example.com")
	h.fire(late)
	h.drain()
	if got := len(h.box.to("pager@example.com")); got != 4 {
		t.Fatalf("after window delivered = %d, want 4", got)
	}
}

func TestLimitSurvivesRestart(t *testing.T) {
	h := newMailHarness(t, 1)
	h.fire(h.rule(t, "one", "a@example.com"))
	h.drain()

	restarted := &Engine{store: h.store, programs: map[string]Program{}, stopped: map[string]bool{}, now: h.engine.now}
	box := &outbox{}
	restarted.EnableEmail(EmailConfig{Send: box.send, HourlyLimit: 1})
	rule := h.rule(t, "two", "a@example.com")
	restarted.transition(rule, "api", true, Env{Service: "api"}, h.now)
	restarted.drainEmail(context.Background())
	if len(box.mails) != 0 {
		t.Fatalf("limit reset by restart: %+v", box.mails)
	}
}

func TestDeletingARuleDoesNotResetTheHourlyLimit(t *testing.T) {
	h := newMailHarness(t, 1)
	first := h.rule(t, "first", "a@example.com")
	h.fire(first)
	h.drain()
	if err := h.store.DeleteRule(first.ID); err != nil {
		t.Fatal(err)
	}
	second := h.rule(t, "first", "a@example.com")
	h.fire(second)
	h.drain()
	if got := h.box.to("a@example.com"); len(got) != 1 {
		t.Fatalf("limit reset by deleting the rule: %+v", got)
	}
	if status := h.ruleStatus(t, second.ID).EmailStatus; status != "suppressed" {
		t.Fatalf("rule status = %q", status)
	}
}

func TestDeletingAnAlertDoesNotResetTheHourlyLimit(t *testing.T) {
	h := newMailHarness(t, 1)
	rule := h.rule(t, "errors", "a@example.com")
	h.fire(rule)
	h.drain()
	alert, err := h.store.GetAlert(rule.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.DeleteAlert(alert.ID); err != nil {
		t.Fatal(err)
	}
	h.fire(rule)
	h.drain()
	if got := h.box.to("a@example.com"); len(got) != 1 {
		t.Fatalf("limit reset by deleting the alert: %+v", got)
	}
}

func TestDeletingARuleCancelsItsQueuedMail(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	h.fire(rule)
	if err := h.store.DeleteRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if len(h.box.mails) != 0 {
		t.Fatalf("mails = %+v", h.box.mails)
	}
}

func TestPruningKeepsSendsInsideTheLimitWindow(t *testing.T) {
	h := newMailHarness(t, 2)
	h.fire(h.rule(t, "old", "a@example.com"))
	h.drain()
	h.now = h.now.Add(2 * time.Hour)
	h.fire(h.rule(t, "recent", "a@example.com"))
	h.drain()
	if err := h.store.pruneEmailDeliveries(7, h.now); err != nil {
		t.Fatal(err)
	}
	var kept int
	if err := h.store.db.QueryRow(`SELECT COUNT(*) FROM alert_email_sends`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Fatalf("sends kept = %d, want 1", kept)
	}
}

func TestStatusDetailLeavesOutServerTextAndAddresses(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	h.box.fail = func(auth.AlertMail) error {
		return fmt.Errorf("auth: send email: %w: 550 private note for a@example.com", &mail.SendError{Reason: mail.ErrSMTPRcptTo})
	}
	h.fire(rule)
	h.drain()
	stored := h.ruleStatus(t, rule.ID)
	if stored.EmailStatus != "retrying" || !strings.Contains(stored.EmailStatusDetail, "RCPT TO") {
		t.Fatalf("rule = %+v", stored)
	}
	for _, leaked := range []string{"private note", "a@example.com"} {
		if strings.Contains(stored.EmailStatusDetail, leaked) {
			t.Fatalf("detail leaks %q: %q", leaked, stored.EmailStatusDetail)
		}
	}
}

func (h *mailHarness) sendsRecorded(t *testing.T) int {
	t.Helper()
	var n int
	if err := h.store.db.QueryRow(`SELECT COUNT(*) FROM alert_email_sends`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (h *mailHarness) exec(t *testing.T, statement string) {
	t.Helper()
	if _, err := h.store.db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptedMailIsRecordedAfterADatabaseFailureWithoutResending(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	h.exec(t, `CREATE TRIGGER block_send BEFORE INSERT ON alert_email_sends BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
	h.fire(rule)
	h.drain()
	h.exec(t, `DROP TRIGGER block_send`)
	h.now = h.now.Add(time.Minute)
	h.drain()
	h.drain()
	if got := h.box.to("a@example.com"); len(got) != 1 {
		t.Fatalf("sends = %d, want 1", len(got))
	}
	if got := h.sendsRecorded(t); got != 1 {
		t.Fatalf("recorded sends = %d, want 1", got)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "sent" {
		t.Fatalf("rule status = %q", status)
	}
}

func TestRetryIsScheduledAfterADatabaseFailureRecordingIt(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	attempts := 0
	h.box.fail = func(auth.AlertMail) error { attempts++; return errors.New("451 later") }
	h.exec(t, `CREATE TRIGGER block_retry BEFORE UPDATE ON alert_email_deliveries WHEN NEW.status = 'pending' BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
	h.fire(rule)
	h.drain()
	h.exec(t, `DROP TRIGGER block_retry`)
	h.drain()
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 before the backoff elapses", attempts)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "retrying" {
		t.Fatalf("rule status = %q", status)
	}
	h.box.fail = nil
	h.now = h.now.Add(2 * time.Minute)
	h.drain()
	if got := h.box.to("a@example.com"); len(got) != 1 {
		t.Fatalf("mails = %+v", got)
	}
}

func TestShutdownJustAfterAnAcceptedSendKeepsTheRecord(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.box.fail = func(auth.AlertMail) error { cancel(); return nil }
	h.fire(rule)
	h.engine.drainEmail(ctx)
	if got := h.sendsRecorded(t); got != 1 {
		t.Fatalf("recorded sends = %d, want 1", got)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "sent" {
		t.Fatalf("rule status = %q", status)
	}
}

func TestRowStrandedInSendingIsRecoveredWhileRunning(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	h.fire(rule)
	h.exec(t, `UPDATE alert_email_deliveries SET status='sending', attempts=1`)
	h.drain()
	if got := h.box.to("a@example.com"); len(got) != 1 {
		t.Fatalf("mails = %+v", got)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "sent" {
		t.Fatalf("rule status = %q", status)
	}
}

func TestRuleDeletedAfterTheBatchReadSendsNothing(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	h.fire(rule)
	rows, err := h.store.q.ListOpenAlertEmailDeliveries(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, %v", rows, err)
	}
	if err := h.store.DeleteRule(rule.ID); err != nil {
		t.Fatal(err)
	}
	h.engine.deliverEmail(context.Background(), rows[0])
	if len(h.box.mails) != 0 {
		t.Fatalf("mailed after the rule was deleted: %+v", h.box.mails)
	}
}

func TestRetryDropsARecipientNoLongerOnTheRule(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "stay@example.com", "gone@example.com")
	h.box.fail = func(m auth.AlertMail) error {
		if m.To == "gone@example.com" {
			return errors.New("451 later")
		}
		return nil
	}
	h.fire(rule)
	h.drain()
	h.box.fail = nil
	rule.EmailRecipients = []string{"stay@example.com"}
	if _, err := h.store.ReplaceRule(rule); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(2 * time.Minute)
	h.drain()
	if got := h.box.to("gone@example.com"); len(got) != 0 {
		t.Fatalf("mailed a removed recipient: %+v", got)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "sent" {
		t.Fatalf("rule status = %q", status)
	}
}

func TestDroppingEveryRecipientClearsOnlyItsOwnRetryStatus(t *testing.T) {
	setup := func(t *testing.T) (*mailHarness, Rule) {
		h := newMailHarness(t, 30)
		rule := h.rule(t, "errors", "gone@example.com", "keep@example.com")
		h.box.fail = func(auth.AlertMail) error { return errors.New("451 later") }
		h.fire(rule)
		h.drain()
		h.box.fail = nil
		if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "retrying" {
			t.Fatalf("rule status = %q", status)
		}
		rule.EmailRecipients = []string{"new@example.com"}
		if _, err := h.store.ReplaceRule(rule); err != nil {
			t.Fatal(err)
		}
		return h, rule
	}

	t.Run("retrying status is cleared", func(t *testing.T) {
		h, rule := setup(t)
		h.now = h.now.Add(2 * time.Minute)
		h.drain()
		if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "" {
			t.Fatalf("rule status = %q", status)
		}
	})

	t.Run("a newer failure stays visible", func(t *testing.T) {
		h, rule := setup(t)
		h.engine.setRuleEmailStatus(context.Background(), rule.ID, "failed", "newer notification")
		h.now = h.now.Add(2 * time.Minute)
		h.drain()
		stored := h.ruleStatus(t, rule.ID)
		if stored.EmailStatus != "failed" || stored.EmailStatusDetail != "newer notification" {
			t.Fatalf("rule = %+v", stored)
		}
	})
}

func TestEmailWorkerKeepsRunningAfterAPanic(t *testing.T) {
	h := newMailHarness(t, 30)
	h.engine.now = nil
	rule := h.rule(t, "errors", "a@example.com")
	panicked := false
	h.box.fail = func(auth.AlertMail) error {
		if !panicked {
			panicked = true
			panic("boom")
		}
		return nil
	}
	h.engine.transition(rule, "api", true, Env{Service: "api"}, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); h.engine.runEmail(ctx) }()
	deadline := time.After(10 * time.Second)
	for len(h.box.to("a@example.com")) != 1 {
		select {
		case <-done:
			t.Fatal("worker exited after a panic")
		case <-deadline:
			t.Fatal("mail was not delivered after the panic")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
}

// startDroppingSMTP accepts mail and drops the connection when dropAt arrives,
// returning the settings and a count of accepted messages.
func startDroppingSMTP(t *testing.T, dropAt string) (auth.SMTPConfig, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	accepted := &atomic.Int32{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				fmt.Fprint(conn, "220 fake\r\n")
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					verb := strings.ToUpper(strings.Fields(line + " x")[0])
					if verb == dropAt {
						return
					}
					switch verb {
					case "EHLO", "HELO":
						fmt.Fprint(conn, "250-fake\r\n250 AUTH CRAM-MD5\r\n")
					case "AUTH":
						fmt.Fprint(conn, "334 PDEyMzQ1QGZha2U+\r\n")
						if _, err := reader.ReadString('\n'); err != nil {
							return
						}
						fmt.Fprint(conn, "235 ok\r\n")
					case "DATA":
						fmt.Fprint(conn, "354 go\r\n")
						for {
							part, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							if part == ".\r\n" {
								break
							}
						}
						accepted.Add(1)
						fmt.Fprint(conn, "250 queued\r\n")
					case "QUIT":
						fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						fmt.Fprint(conn, "250 ok\r\n")
					}
				}
			}()
		}
	}()
	return auth.SMTPConfig{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, User: "u", Pass: "p", From: "noreply@example.com"}, accepted
}

func TestAcceptedMailIsSentAndCountedOnceWhenTheSessionEndsBadly(t *testing.T) {
	for _, verb := range []string{"RSET", "QUIT"} {
		t.Run(verb, func(t *testing.T) {
			h := newMailHarness(t, 30)
			cfg, accepted := startDroppingSMTP(t, verb)
			h.engine.email.Send = func(ctx context.Context, m auth.AlertMail) error { return auth.SendAlert(ctx, cfg, m) }
			rule := h.rule(t, "errors", "a@example.com")
			h.fire(rule)
			for range 3 {
				h.drain()
				h.now = h.now.Add(time.Hour)
			}
			if got := accepted.Load(); got != 1 {
				t.Fatalf("accepted = %d, want 1", got)
			}
			if got := h.sendsRecorded(t); got != 1 {
				t.Fatalf("recorded sends = %d, want 1", got)
			}
			if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "sent" {
				t.Fatalf("rule status = %q", status)
			}
		})
	}
}

func TestRestartResumesQueuedMailAndDoesNotResendFinished(t *testing.T) {
	h := newMailHarness(t, 30)
	done := h.rule(t, "done", "done@example.com")
	h.fire(done)
	h.drain()

	interrupted := h.rule(t, "interrupted", "cut@example.com")
	h.fire(interrupted)
	if _, err := h.store.db.Exec(`UPDATE alert_email_deliveries SET status='sending', attempts=1 WHERE recipient='cut@example.com'`); err != nil {
		t.Fatal(err)
	}
	queued := h.rule(t, "queued", "wait@example.com")
	h.fire(queued)

	restarted := &Engine{store: h.store, programs: map[string]Program{}, stopped: map[string]bool{}, now: h.engine.now}
	box := &outbox{}
	restarted.EnableEmail(EmailConfig{Send: box.send, HourlyLimit: 30})
	restarted.recoverEmail(context.Background())
	restarted.drainEmail(context.Background())

	if len(box.to("done@example.com")) != 0 {
		t.Fatal("finished mail was resent")
	}
	if len(box.to("cut@example.com")) != 1 || len(box.to("wait@example.com")) != 1 {
		t.Fatalf("mails = %+v", box.mails)
	}
	restarted.drainEmail(context.Background())
	if len(box.mails) != 2 {
		t.Fatalf("resent on a later drain: %+v", box.mails)
	}
}

func TestResentMailKeepsItsMessageIdentity(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	var ids []string
	h.box.fail = func(m auth.AlertMail) error {
		ids = append(ids, m.ID)
		return errors.New("timeout after DATA")
	}
	h.fire(rule)
	h.drain()
	h.now = h.now.Add(2 * time.Minute)
	h.drain()
	if len(ids) != 2 || ids[0] != ids[1] {
		t.Fatalf("ids = %v", ids)
	}
}

func TestInterruptedLastAttemptFailsInsteadOfSendingAgain(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	h.fire(rule)
	if _, err := h.store.db.Exec(`UPDATE alert_email_deliveries SET status='sending', attempts=?`, len(emailBackoff)); err != nil {
		t.Fatal(err)
	}
	h.engine.recoverEmail(context.Background())
	h.drain()
	if len(h.box.mails) != 0 {
		t.Fatalf("mails = %+v", h.box.mails)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "failed" {
		t.Fatalf("rule status = %q", status)
	}
}

func TestEmailQueuedAtomicallyWithTransition(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	if _, err := h.store.db.Exec(`CREATE TRIGGER block_mail BEFORE INSERT ON alert_email_deliveries BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	h.fire(rule)
	if _, err := h.store.GetAlert(rule.ID, "api"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("alert saved without its email: %v", err)
	}
}

func TestRepeatsFollowTheRulesNotificationBehaviour(t *testing.T) {
	h := newMailHarness(t, 30)
	rule, err := h.store.CreateRule(Rule{Name: "errors", Enabled: true, Service: "api", Expression: "true", RepeatIntervalS: 600, EmailRecipients: []string{"a@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	h.fire(rule)
	h.drain()
	h.now = h.now.Add(5 * time.Minute)
	h.fire(rule)
	h.drain()
	if got := h.box.to("a@example.com"); len(got) != 1 {
		t.Fatalf("repeated inside the interval: %+v", got)
	}
	h.now = h.now.Add(6 * time.Minute)
	h.fire(rule)
	h.drain()
	got := h.box.to("a@example.com")
	if len(got) != 2 || got[1].Event != "reminder" {
		t.Fatalf("reminder mails = %+v", got)
	}

	// notify_on_resolve is off, so the resolve is silent.
	h.now = h.now.Add(time.Minute)
	h.engine.transition(rule, "api", false, Env{Service: "api"}, h.now)
	h.drain()
	if got := h.box.to("a@example.com"); len(got) != 2 {
		t.Fatalf("resolve mailed with notify_on_resolve off: %+v", got)
	}

	rule.NotifyOnResolve = true
	h.now = h.now.Add(time.Minute)
	h.fire(rule)
	h.now = h.now.Add(time.Minute)
	h.engine.transition(rule, "api", false, Env{Service: "api"}, h.now)
	h.drain()
	got = h.box.to("a@example.com")
	if len(got) != 4 || got[3].Event != "resolved" {
		t.Fatalf("resolve mails = %+v", got)
	}
}

func TestDisablingAFiringRuleMailsItsResolve(t *testing.T) {
	h := newMailHarness(t, 30)
	rule, err := h.store.CreateRule(Rule{Name: "errors", Enabled: true, Service: "api", Expression: "true", NotifyOnResolve: true, EmailRecipients: []string{"a@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	h.fire(rule)
	h.engine.DisableRule(rule)
	h.drain()
	got := h.box.to("a@example.com")
	if len(got) != 2 || got[1].Event != "resolved" {
		t.Fatalf("mails = %+v", got)
	}
}

func TestWebhookResultStaysOnAlertWhenRuleAlsoEmails(t *testing.T) {
	var calls sync.WaitGroup
	calls.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defer calls.Done()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	h := newMailHarness(t, 30)
	rule, err := h.store.CreateRule(Rule{Name: "both", Enabled: true, Service: "api", Expression: "true", WebhookURL: srv.URL, EmailRecipients: []string{"a@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	h.box.fail = func(auth.AlertMail) error { return fmt.Errorf("%w: rejected", auth.ErrInvalidMailAddress) }
	h.fire(rule)
	calls.Wait()
	h.drain()

	deadline := time.Now().Add(5 * time.Second)
	for {
		alert, err := h.store.GetAlert(rule.ID, "api")
		if err != nil {
			t.Fatal(err)
		}
		if alert.LastDeliveryStatus == "success" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("alert status = %q, want the webhook's success", alert.LastDeliveryStatus)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status := h.ruleStatus(t, rule.ID).EmailStatus; status != "failed" {
		t.Fatalf("rule email status = %q", status)
	}
}

func TestWithoutSMTPSavedRecipientsReportUnavailable(t *testing.T) {
	engine, store := newTestEngine(t)
	rule, err := store.CreateRule(Rule{Name: "errors", Enabled: true, Expression: "true", EmailRecipients: []string{"a@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	got := engine.WithEmailState(rule)
	if got.EmailStatus != "unavailable" || !strings.Contains(got.EmailStatusDetail, "SMTP") {
		t.Fatalf("rule = %+v", got)
	}
	if engine.EmailOptions().Available {
		t.Fatal("email reported available without SMTP")
	}
	plain, err := store.CreateRule(Rule{Name: "plain", Enabled: true, Expression: "true"})
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.WithEmailState(plain); got.EmailStatus != "" {
		t.Fatalf("rule without email = %+v", got)
	}
}

func TestMailQueuedBeforeSMTPWasRemovedIsMarkedFailed(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors", "a@example.com")
	h.fire(rule)

	without := &Engine{store: h.store, programs: map[string]Program{}, stopped: map[string]bool{}}
	without.failUndeliverableEmail(context.Background())
	stored := h.ruleStatus(t, rule.ID)
	if stored.EmailStatus != "failed" || !strings.Contains(stored.EmailStatusDetail, "SMTP") {
		t.Fatalf("rule = %+v", stored)
	}
}

func TestRuleWithoutEmailDestinationHasNoEmailState(t *testing.T) {
	h := newMailHarness(t, 30)
	rule := h.rule(t, "errors")
	rule.EmailStatus = "failed"
	if got := h.engine.WithEmailState(rule); got.EmailStatus != "" {
		t.Fatalf("rule = %+v", got)
	}
}
