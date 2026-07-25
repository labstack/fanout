package auth

import (
	"bytes"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/brand"
)

func TestCodeMailUsesFanoutBrandAndEscapesCode(t *testing.T) {
	body, err := renderMail(codeMailTemplate, mailData{
		Preheader: `Sign in <now>`,
		Code:      `<script>alert("x")</script>`,
	})
	if err != nil {
		t.Fatalf("renderMail: %v", err)
	}

	for _, want := range []string{
		"Fanout",
		"Secure sign in",
		"Your verification code",
		brand.EmailMarkHTML,
		"Sign in &lt;now&gt;",
		"&lt;script&gt;",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("code email missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `<script>`) {
		t.Fatalf("code email contains unescaped script markup:\n%s", body)
	}
}

func TestMailMessageContainsPlainTextAndHTMLAlternatives(t *testing.T) {
	message, err := newMailMessage(
		"Fanout <fanout@labstack.com>",
		"operator@example.com",
		"Fanout login code: 123456",
		"Your Fanout verification code is 123456.",
		"<strong>Your Fanout verification code is 123456.</strong>",
	)
	if err != nil {
		t.Fatalf("newMailMessage: %v", err)
	}

	var raw bytes.Buffer
	if _, err := message.WriteTo(&raw); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	for _, want := range []string{
		"Content-Type: multipart/alternative;",
		"Content-Type: text/plain;",
		"Content-Type: text/html;",
	} {
		if !strings.Contains(raw.String(), want) {
			t.Fatalf("serialized email missing %q:\n%s", want, raw.String())
		}
	}
}

func TestPlainTextMailBodiesIncludeSafetyNotice(t *testing.T) {
	for name, body := range map[string]string{
		"code":   codeMailText("123456"),
		"invite": inviteMailText,
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(body, plainTextSafetyNotice) {
				t.Fatalf("plain-text %s email is missing the safety notice:\n%s", name, body)
			}
		})
	}
}

func TestInviteMailUsesFanoutBrand(t *testing.T) {
	body, err := renderMail(inviteMailTemplate, mailData{
		Preheader: "You've been invited to a Fanout workspace.",
	})
	if err != nil {
		t.Fatalf("renderMail: %v", err)
	}

	for _, want := range []string{
		"Fanout",
		"Workspace invitation",
		"You've been invited to Fanout",
		"start investigating your observability data",
		"Fanout observability",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("invite email missing %q:\n%s", want, body)
		}
	}
}
