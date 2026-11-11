package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	mail "github.com/wneessen/go-mail"
)

// AlertMail is one alert notification for one recipient. Every text field may
// hold telemetry-derived or operator-written values; SendAlert neutralizes them.
type AlertMail struct {
	// ID identifies the delivery. It becomes the Message-ID, so a resend after
	// an interrupted attempt carries the same one.
	ID          string
	To          string
	Rule        string
	Description string
	Service     string
	Event       string
	Value       float64
	Time        time.Time
	// LimitNotice, when set, warns that later notifications will be suppressed.
	LimitNotice string
}

// ErrInvalidMailAddress marks a sender or recipient that does not parse.
var ErrInvalidMailAddress = errors.New("invalid mail address")

// IsPermanentMailError reports whether retrying err cannot succeed: an address
// that does not parse, or a 5xx SMTP reply. Everything else is transient,
// including a connection lost mid-session, which go-mail reports as a SendError
// with no server code and does not count as temporary.
func IsPermanentMailError(err error) bool {
	if errors.Is(err, ErrInvalidMailAddress) {
		return true
	}
	var sendErr *mail.SendError
	if !errors.As(err, &sendErr) {
		return false
	}
	code := sendErr.ErrorCode()
	return code >= 500 && code < 600
}

// MailFailureDetail describes a send failure by its class and SMTP code, for
// logs and status shown to users. It never includes err's text, which can carry
// server responses and recipient addresses.
func MailFailureDetail(err error) string {
	var sendErr *mail.SendError
	var netErr net.Error
	switch {
	case errors.Is(err, ErrInvalidMailAddress):
		return "Invalid email address"
	case errors.As(err, &sendErr):
		if code := sendErr.ErrorCode(); code != 0 {
			return fmt.Sprintf("Mail server returned %d while %s", code, sendErr.Reason)
		}
		return fmt.Sprintf("Delivery failed while %s", sendErr.Reason)
	case errors.Is(err, context.DeadlineExceeded):
		return "Timed out talking to the mail server"
	case errors.As(err, &netErr):
		return "Could not reach the mail server"
	default:
		return "The exchange with the mail server failed"
	}
}

// SendAlert renders and sends one alert notification.
func SendAlert(ctx context.Context, cfg SMTPConfig, alert AlertMail) error {
	subject, text, html, err := renderAlertMail(alert)
	if err != nil {
		return err
	}
	message, err := newMailMessage(cfg.From, alert.To, subject, text, html)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidMailAddress, err)
	}
	message.SetMessageIDWithValue(alert.ID + "@alerts.pitot")
	return deliver(ctx, cfg, message)
}

// Lengths, in runes, past which a value is cut. They keep a hostile value from
// filling a subject line or a message.
const (
	maxAlertNameLength        = 120
	maxAlertDescriptionLength = 1000
)

// singleLine makes a value safe for a header or one line of text: control,
// format and line-separator characters become spaces, so CR/LF cannot start a
// new header or forge a line, and whitespace runs collapse.
func singleLine(value string, limit int) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(value, " "))
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if utf8.RuneCountInString(cleaned) > limit {
		cleaned = strings.TrimSpace(string([]rune(cleaned)[:limit])) + "…"
	}
	return cleaned
}

type alertMailData struct {
	Preheader   string
	Label       string
	Rule        string
	Description string
	Service     string
	Value       string
	Time        string
	LimitNotice string
}

func alertEventLabel(event string) string {
	switch event {
	case "firing":
		return "Firing"
	case "reminder":
		return "Still firing"
	case "resolved":
		return "Resolved"
	default:
		return "Alert"
	}
}

func renderAlertMail(alert AlertMail) (subject, text, html string, err error) {
	data := alertMailData{
		Label:       alertEventLabel(alert.Event),
		Rule:        singleLine(alert.Rule, maxAlertNameLength),
		Description: singleLine(alert.Description, maxAlertDescriptionLength),
		Service:     singleLine(alert.Service, maxAlertNameLength),
		Value:       strconv.FormatFloat(alert.Value, 'g', 6, 64),
		Time:        alert.Time.UTC().Format(time.RFC1123),
		LimitNotice: singleLine(alert.LimitNotice, maxAlertDescriptionLength),
	}
	subject = fmt.Sprintf("[Pitot] %s: %s on %s", data.Label, data.Rule, data.Service)
	data.Preheader = subject

	var body strings.Builder
	fmt.Fprintf(&body, "%s\n\nRule: %s\nService: %s\nValue: %s\nTime: %s\n", data.Label, data.Rule, data.Service, data.Value, data.Time)
	if data.Description != "" {
		fmt.Fprintf(&body, "Description: %s\n", data.Description)
	}
	if data.LimitNotice != "" {
		fmt.Fprintf(&body, "\n%s\n", data.LimitNotice)
	}

	var rendered bytes.Buffer
	if err := alertMailTemplate.Execute(&rendered, data); err != nil {
		return "", "", "", fmt.Errorf("auth: render alert email: %w", err)
	}
	return subject, body.String(), rendered.String(), nil
}

var alertMailTemplate = template.Must(template.New("alert-email").Parse(mailShellStart + `
<p style="margin:0 0 8px;color:#087f5b;font-size:12px;font-weight:700;letter-spacing:.12em;text-transform:uppercase">{{.Label}}</p>
<h1 style="margin:0;color:#1f2923;font-size:26px;font-weight:650;line-height:1.2;letter-spacing:-.02em">{{.Rule}}</h1>
<p style="margin:16px 0 0;color:#66736c;font-size:16px;line-height:1.6">Service: {{.Service}}<br>Value: {{.Value}}<br>Time: {{.Time}}</p>
{{if .Description}}<p style="margin:16px 0 0;color:#66736c;font-size:16px;line-height:1.6">{{.Description}}</p>{{end}}
{{if .LimitNotice}}<p style="margin:16px 0 0;color:#66736c;font-size:14px;line-height:1.6">{{.LimitNotice}}</p>{{end}}
` + mailShellEnd))
