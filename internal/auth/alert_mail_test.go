package auth

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP accepts one conversation at a time and records message data. A
// nonempty rcptReply replaces the normal reply to RCPT.
type fakeSMTP struct {
	listener  net.Listener
	rcptReply string
	// dropAt closes the connection without a reply when this verb arrives;
	// BODY drops after the message data, before the server's reply.
	dropAt string
	mu     sync.Mutex
	data   []string
}

func startFakeSMTP(t *testing.T, rcptReply string) (*fakeSMTP, SMTPConfig) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeSMTP{listener: listener, rcptReply: rcptReply}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go server.serve(conn)
		}
	}()
	return server, SMTPConfig{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, User: "user", Pass: "pass", From: "Fanout <noreply@example.com>"}
}

func (s *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	reply := func(line string) { fmt.Fprintf(conn, "%s\r\n", line) }
	reply("220 fake ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		if verb == s.dropAt {
			return
		}
		switch verb {
		case "EHLO", "HELO":
			reply("250-fake")
			reply("250 AUTH CRAM-MD5")
		case "AUTH":
			reply("334 PDEyMzQ1QGZha2U+")
			if _, err := reader.ReadString('\n'); err != nil {
				return
			}
			reply("235 ok")
		case "RCPT":
			if s.rcptReply != "" {
				reply(s.rcptReply)
				continue
			}
			reply("250 ok")
		case "DATA":
			reply("354 go")
			var body strings.Builder
			for {
				part, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if part == ".\r\n" {
					break
				}
				body.WriteString(strings.TrimPrefix(part, "."))
			}
			s.mu.Lock()
			s.data = append(s.data, body.String())
			s.mu.Unlock()
			if s.dropAt == "BODY" {
				return
			}
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (s *fakeSMTP) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.data...)
}

const hostileService = "api\r\nBcc: victim@evil.test\r\n\r\n<script>alert(1)</script>\u2028Subject: forged\u202e"

func hostileAlert() AlertMail {
	return AlertMail{
		ID: "0190-delivery", To: "oncall@example.com", Rule: "High errors\r\nX-Injected: yes", Description: "<img src=x onerror=alert(2)>\nline two",
		Service: hostileService, Event: "firing", Value: 0.5, Time: time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC),
	}
}

func decodeParts(t *testing.T, message *mail.Message) (text, html string) {
	t.Helper()
	_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(message.Body, params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			return text, html
		}
		if err != nil {
			t.Fatal(err)
		}
		var source io.Reader = part
		switch strings.ToLower(part.Header.Get("Content-Transfer-Encoding")) {
		case "quoted-printable":
			source = quotedprintable.NewReader(part)
		case "base64":
			source = base64.NewDecoder(base64.StdEncoding, stripNewlines{part})
		}
		content, err := io.ReadAll(source)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(part.Header.Get("Content-Type"), "text/html") {
			html = string(content)
		} else {
			text = string(content)
		}
	}
}

type stripNewlines struct{ reader io.Reader }

func (s stripNewlines) Read(p []byte) (int, error) {
	n, err := s.reader.Read(p)
	kept := 0
	for _, b := range p[:n] {
		if b != '\r' && b != '\n' {
			p[kept] = b
			kept++
		}
	}
	return kept, err
}

func TestSendAlertKeepsHostileValuesOutOfHeadersAndMarkup(t *testing.T) {
	server, cfg := startFakeSMTP(t, "")
	if err := SendAlert(context.Background(), cfg, hostileAlert()); err != nil {
		t.Fatal(err)
	}
	messages := server.messages()
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	message, err := mail.ReadMessage(strings.NewReader(messages[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"Bcc", "X-Injected"} {
		if value := message.Header.Get(header); value != "" {
			t.Fatalf("injected header %s: %q", header, value)
		}
	}
	if got := message.Header[textproto.CanonicalMIMEHeaderKey("Subject")]; len(got) != 1 {
		t.Fatalf("subject headers = %q", got)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(subject, "[Fanout] Firing: High errors X-Injected: yes on api Bcc:") {
		t.Fatalf("subject = %q", subject)
	}
	if strings.ContainsAny(subject, "\r\n\u2028\u202e") {
		t.Fatalf("subject holds control characters: %q", subject)
	}
	if got := message.Header.Get("Message-Id"); got != "<0190-delivery@alerts.fanout>" {
		t.Fatalf("message id = %q", got)
	}

	text, html := decodeParts(t, message)
	if !strings.Contains(text, "Service: api Bcc: victim@evil.test <script>") {
		t.Fatalf("text = %q", text)
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "Bcc:") || strings.HasPrefix(line, "Subject:") {
			t.Fatalf("value forged a line: %q", text)
		}
	}
	for _, raw := range []string{"<script>", "<img src"} {
		if strings.Contains(html, raw) {
			t.Fatalf("html holds unescaped %q: %s", raw, html)
		}
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("html did not escape: %s", html)
	}
}

func TestSendAlertRejectsAddressWithLineBreakWithoutSending(t *testing.T) {
	server, cfg := startFakeSMTP(t, "")
	alert := hostileAlert()
	alert.To = "a@example.com\r\nBcc: victim@evil.test"
	err := SendAlert(context.Background(), cfg, alert)
	if err == nil || !IsPermanentMailError(err) {
		t.Fatalf("err = %v, want permanent", err)
	}
	if len(server.messages()) != 0 {
		t.Fatal("message was sent")
	}
}

func TestMailErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name, reply, drop string
		permanent         bool
	}{
		{name: "mailbox rejected", reply: "550 no such user", permanent: true},
		{name: "greylisted", reply: "451 try again later"},
		// A lost connection is a SendError with no server code: retryable.
		{name: "dropped at MAIL", drop: "MAIL"},
		{name: "dropped at RCPT", drop: "RCPT"},
		{name: "dropped at DATA", drop: "DATA"},
		{name: "dropped after the body", drop: "BODY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, cfg := startFakeSMTP(t, tc.reply)
			server.dropAt = tc.drop
			err := SendAlert(context.Background(), cfg, hostileAlert())
			if err == nil {
				t.Fatal("send succeeded")
			}
			if got := IsPermanentMailError(err); got != tc.permanent {
				t.Fatalf("permanent = %v, want %v: %v", got, tc.permanent, err)
			}
		})
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	err = SendAlert(context.Background(), SMTPConfig{Host: "127.0.0.1", Port: port, User: "u", Pass: "p", From: "a@example.com"}, hostileAlert())
	if err == nil || IsPermanentMailError(err) {
		t.Fatalf("connection failure = %v, want transient", err)
	}
}

// Once the server has accepted the message, a failure while closing the session
// must not report the send as failed, or the message would be sent again.
func TestAcceptedMailSucceedsWhenTheSessionEndsBadly(t *testing.T) {
	for _, verb := range []string{"RSET", "QUIT"} {
		t.Run(verb, func(t *testing.T) {
			server, cfg := startFakeSMTP(t, "")
			server.dropAt = verb
			if err := SendAlert(context.Background(), cfg, hostileAlert()); err != nil {
				t.Fatalf("accepted mail reported as failed: %v", err)
			}
			if got := len(server.messages()); got != 1 {
				t.Fatalf("messages = %d, want 1", got)
			}
		})
	}
}

func TestMailFailureDetailKeepsTheCodeAndDropsServerText(t *testing.T) {
	_, cfg := startFakeSMTP(t, "550 private note about oncall@example.com")
	err := SendAlert(context.Background(), cfg, hostileAlert())
	if err == nil {
		t.Fatal("send succeeded")
	}
	detail := MailFailureDetail(err)
	if !strings.Contains(detail, "550") || !strings.Contains(detail, "RCPT TO") {
		t.Fatalf("detail = %q", detail)
	}
	for _, leaked := range []string{"private note", "oncall@example.com"} {
		if strings.Contains(detail, leaked) {
			t.Fatalf("detail leaks %q: %q", leaked, detail)
		}
	}
	if got := MailFailureDetail(fmt.Errorf("%w: a@example.com", ErrInvalidMailAddress)); strings.Contains(got, "a@example.com") {
		t.Fatalf("address detail = %q", got)
	}
	if got := MailFailureDetail(fmt.Errorf("dial a@example.com: %w", context.DeadlineExceeded)); strings.Contains(got, "a@example.com") {
		t.Fatalf("timeout detail = %q", got)
	}
}

func TestSingleLineCapsLengthAndCollapsesWhitespace(t *testing.T) {
	got := singleLine(strings.Repeat("a", 500)+"\x00", maxAlertNameLength)
	if len([]rune(got)) != maxAlertNameLength+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("got %q", got)
	}
	if got := singleLine(" a\t\tb\xff\n c ", 100); got != "a b c" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeRecipients(t *testing.T) {
	got, err := NormalizeRecipients([]string{" A@Example.com ", "a@example.com", "b@example.com"})
	if err != nil || len(got) != 2 || got[0] != "a@example.com" || got[1] != "b@example.com" {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{
		"", "not-an-address", "Name <a@example.com>", "a@example.com\r\nBcc: x@evil.test",
		"a\r\n@example.com", "a@example.com, b@example.com", strings.Repeat("a", 250) + "@example.com",
	} {
		if _, err := NormalizeRecipients([]string{bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	many := make([]string, MaxAlertEmailRecipients+1)
	for i := range many {
		many[i] = fmt.Sprintf("a%d@example.com", i)
	}
	if _, err := NormalizeRecipients(many); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("over cap = %v", err)
	}
	if got, err := NormalizeRecipients(many[:MaxAlertEmailRecipients]); err != nil || len(got) != MaxAlertEmailRecipients {
		t.Fatalf("at cap = %v, %v", got, err)
	}
}
