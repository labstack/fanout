package auth

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"time"

	mail "github.com/wneessen/go-mail"

	"github.com/labstack/fanout/internal/brand"
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
	message, err := renderMail(inviteMailTemplate, mailData{
		Preheader: "You've been invited to a Fanout workspace.",
	})
	if err != nil {
		return err
	}
	return send(cfg, to, "You've been invited to Fanout", inviteMailText, message)
}

// SendCode sends a verification code email via SMTP.
func SendCode(cfg SMTPConfig, to, code string) error {
	subject := fmt.Sprintf("Fanout login code: %s", code)
	message, err := renderMail(codeMailTemplate, mailData{
		Preheader: "Your Fanout verification code is " + code + ".",
		Code:      code,
	})
	if err != nil {
		return err
	}
	return send(cfg, to, subject, codeMailText(code), message)
}

const plainTextSafetyNotice = "If you did not expect this message, you can safely ignore it."

const inviteMailText = "You've been invited to Fanout.\n\nVisit your Fanout instance and sign in with this email address to start investigating your observability data.\n\n" + plainTextSafetyNotice

func codeMailText(code string) string {
	return fmt.Sprintf("Your Fanout verification code is %s.\n\nEnter this code to finish signing in. It expires in 5 minutes.\n\n%s", code, plainTextSafetyNotice)
}

func send(cfg SMTPConfig, to, subject, bodyText, bodyHTML string) error {
	message, err := newMailMessage(cfg.From, to, subject, bodyText, bodyHTML)
	if err != nil {
		return err
	}

	options := []mail.Option{
		mail.WithPort(cfg.Port),
		mail.WithTimeout(10 * time.Second),
		mail.WithUsername(cfg.User),
		mail.WithPassword(cfg.Pass),
		mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover),
	}
	if cfg.Port == 465 {
		options = append(options, mail.WithSSL())
	} else {
		options = append(options, mail.WithTLSPolicy(mail.TLSOpportunistic))
	}
	client, err := mail.NewClient(cfg.Host, options...)
	if err != nil {
		return fmt.Errorf("auth: configure SMTP client: %w", err)
	}
	if err := client.DialAndSendWithContext(context.Background(), message); err != nil {
		return fmt.Errorf("auth: send email: %w", err)
	}
	return nil
}

func newMailMessage(from, to, subject, bodyText, bodyHTML string) (*mail.Msg, error) {
	message := mail.NewMsg()
	if err := message.From(from); err != nil {
		return nil, fmt.Errorf("auth: invalid SMTP sender: %w", err)
	}
	if err := message.To(to); err != nil {
		return nil, fmt.Errorf("auth: invalid email recipient: %w", err)
	}
	message.Subject(subject)
	message.SetBodyString(mail.TypeTextPlain, bodyText)
	message.AddAlternativeString(mail.TypeTextHTML, bodyHTML)
	return message, nil
}

type mailData struct {
	Preheader string
	Code      string
}

func renderMail(t *template.Template, data mailData) (string, error) {
	var body bytes.Buffer
	if err := t.Execute(&body, data); err != nil {
		return "", fmt.Errorf("auth: render email: %w", err)
	}
	return body.String(), nil
}

const mailShellStart = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Fanout</title></head>
<body style="margin:0;padding:0;background:#f4f8f6;color:#1f2923;font-family:Inter,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif">
<div style="display:none;max-height:0;overflow:hidden;color:transparent">{{.Preheader}}</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;background:#f4f8f6"><tr><td align="center" style="padding:48px 16px">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="width:100%;max-width:560px;border-collapse:separate;background:#ffffff;border:1px solid #e1e7e3;border-radius:24px;box-shadow:0 18px 45px rgba(31,41,55,.08)">
<tr><td style="padding:36px 40px 16px">
<table role="presentation" cellpadding="0" cellspacing="0"><tr><td style="width:46px;vertical-align:middle">` + brand.EmailMarkHTML + `</td><td style="padding-left:14px;vertical-align:middle;font-size:18px;font-weight:800;line-height:1;letter-spacing:.17em;text-transform:uppercase;color:#1f2923">Fanout</td></tr></table>
</td></tr><tr><td style="padding:12px 40px 40px">`

const mailShellEnd = `<p style="margin:28px 0 0;color:#748078;font-size:13px;line-height:1.6">If you did not expect this message, you can safely ignore it.</p>
</td></tr></table><p style="margin:20px 0 0;color:#8a9690;font-size:12px;line-height:1.5">Fanout observability</p>
</td></tr></table></body></html>`

var codeMailTemplate = template.Must(template.New("code-email").Parse(mailShellStart + `
<p style="margin:0 0 8px;color:#087f5b;font-size:12px;font-weight:700;letter-spacing:.12em;text-transform:uppercase">Secure sign in</p>
<h1 style="margin:0;color:#1f2923;font-size:30px;font-weight:650;line-height:1.15;letter-spacing:-.025em">Your verification code</h1>
<p style="margin:16px 0 24px;color:#66736c;font-size:16px;line-height:1.6">Enter this code to finish signing in to Fanout. It expires in 5 minutes.</p>
<div style="padding:18px 20px;border:1px solid #cfe7dd;border-radius:14px;background:#f0fbf7;color:#087f5b;font-family:ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace;font-size:30px;font-weight:700;line-height:1;letter-spacing:.22em;text-align:center">{{.Code}}</div>
` + mailShellEnd))

var inviteMailTemplate = template.Must(template.New("invite-email").Parse(mailShellStart + `
<p style="margin:0 0 8px;color:#087f5b;font-size:12px;font-weight:700;letter-spacing:.12em;text-transform:uppercase">Workspace invitation</p>
<h1 style="margin:0;color:#1f2923;font-size:30px;font-weight:650;line-height:1.15;letter-spacing:-.025em">You've been invited to Fanout</h1>
<p style="margin:16px 0 0;color:#66736c;font-size:16px;line-height:1.6">Visit your Fanout instance and sign in with this email address to start investigating your observability data.</p>
` + mailShellEnd))
