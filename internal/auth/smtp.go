package auth

import (
	"fmt"
	"net/smtp"
	"strconv"
	"time"

	"github.com/labstack/gommon/email"
)

// SMTPConfig holds SMTP connection settings.
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

// SendInvite sends an invitation email to a new user.
func SendInvite(cfg SMTPConfig, to string) error {
	subject := "You've been invited to Fanout"
	body := "You've been invited to Fanout.\n\nVisit your Fanout instance and sign in with this email address to get started."
	return send(cfg, to, subject, body)
}

// SendCode sends a verification code email via SMTP.
func SendCode(cfg SMTPConfig, to, code string) error {
	subject := fmt.Sprintf("Fanout login code: %s", code)
	body := fmt.Sprintf(
		"Your Fanout verification code is:\n\n  %s\n\nThis code expires in 5 minutes.\n\nIf you did not request this, you can safely ignore this email.",
		code,
	)
	return send(cfg, to, subject, body)
}

func send(cfg SMTPConfig, to, subject, body string) error {
	e := email.New(cfg.Host + ":" + strconv.Itoa(cfg.Port))
	e.Auth = smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	e.DialTimeout = 10 * time.Second
	return e.Send(&email.Message{
		From:     cfg.From,
		To:       to,
		Subject:  subject,
		BodyText: body,
	})
}
