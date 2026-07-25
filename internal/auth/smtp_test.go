package auth

import (
	"strings"
	"testing"
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
		"background:#70dfc3",
		"background:#4aaef2",
		"background:#9a50f4",
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
