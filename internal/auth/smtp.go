package auth

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
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
	return sendMail(cfg, to, subject, body)
}

// SendCode sends a verification code email via SMTP.
func SendCode(cfg SMTPConfig, to, code string) error {
	subject := fmt.Sprintf("Fanout login code: %s", code)
	body := fmt.Sprintf(
		"Your Fanout verification code is:\n\n  %s\n\nThis code expires in 5 minutes.\n\nIf you did not request this, you can safely ignore this email.",
		code,
	)
	return sendMail(cfg, to, subject, body)
}

func sendMail(cfg SMTPConfig, to, subject, body string) error {
	msg := strings.Join([]string{
		"From: " + cfg.From,
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)

	// Port 465 is SMTPS (implicit TLS). Go's stdlib smtp.SendMail only
	// supports plain + STARTTLS (587/25), so dial TLS ourselves.
	if cfg.Port == 465 {
		return sendImplicitTLS(addr, cfg.Host, auth, cfg.From, []string{to}, []byte(msg))
	}
	return smtp.SendMail(addr, auth, cfg.From, []string{to}, []byte(msg))
}

func sendImplicitTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("smtp: dial TLS: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: new client: %w", err)
	}
	defer client.Close()

	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("smtp: auth: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp: MAIL: %w", err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp: RCPT %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: close body: %w", err)
	}
	return client.Quit()
}
