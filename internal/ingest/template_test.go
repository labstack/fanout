package ingest

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// normalizeText
// ---------------------------------------------------------------------------

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "UUID",
			input: "request id=550e8400-e29b-41d4-a716-446655440000 failed",
			want:  "request id=<uuid> failed",
		},
		{
			name:  "timestamp with offset",
			input: "at 2024-01-15T13:45:00.123+05:30 the event fired",
			want:  "at <time> the event fired",
		},
		{
			name:  "timestamp with Z",
			input: "logged at 2023-06-01T00:00:00Z",
			want:  "logged at <time>",
		},
		{
			name:  "IPv4",
			input: "connected from 192.168.1.42 ok",
			want:  "connected from <ip> ok",
		},
		{
			name:  "IPv4 with port",
			input: "peer 10.0.0.1:8080 accepted",
			want:  "peer <ip> accepted",
		},
		{
			name:  "email",
			input: "user alice@example.com signed in",
			want:  "user <email> signed in",
		},
		{
			name:  "hex 8+ chars",
			input: "commit abcdef12 merged",
			want:  "commit <hex> merged",
		},
		{
			name:  "hex 32-char trace id",
			input: "trace=4bf92f3577b34da6a3ce929d0e0e4736 span=00f067aa0ba902b7",
			want:  "trace=<hex> span=<hex>",
		},
		{
			name:  "0x prefix hex",
			input: "address 0xdeadbeef loaded",
			want:  "address <hex> loaded",
		},
		{
			name:  "path",
			input: "GET /api/users returned 200",
			want:  "GET <path> returned <num>",
		},
		{
			name:  "path with query string",
			input: "request /v1/search?q=hello&limit=10 processed",
			want:  "request <path> processed",
		},
		{
			name:  "quoted string",
			input: `error: "file not found" in handler`,
			want:  "error: <str> in handler",
		},
		{
			name:  "number",
			input: "retried 3 times took 142 ms",
			want:  "retried <num> times took <num> ms",
		},
		{
			name:  "combined patterns",
			input: "user 550e8400-e29b-41d4-a716-446655440000 from 10.0.0.1 at 2024-03-01T12:00:00Z",
			want:  "user <uuid> from <ip> at <time>",
		},
		{
			name:  "empty body",
			input: "",
			want:  "",
		},
		{
			name:  "no-variable passthrough",
			input: "server started successfully",
			want:  "server started successfully",
		},
		{
			// Version strings like 1.2.3.4 match the IPv4 pattern — known trade-off.
			name:  "version number matches as IP",
			input: "version 1.2.3.4 released",
			want:  "version <ip> released",
		},
		{
			// UUID must be matched before hex (UUID contains hex chars), and
			// hex before number (hex contains digit runs).
			name:  "UUID-before-hex-before-number ordering",
			input: "id=550e8400-e29b-41d4-a716-446655440000 ref=deadbeef01 count=7",
			want:  "id=<uuid> ref=<hex> count=<num>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeText(tt.input)
			if got != tt.want {
				t.Errorf("normalizeText(%q)\n got  %q\n want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeJSON
// ---------------------------------------------------------------------------

func TestNormalizeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "preserves keys normalizes string values",
			input: `{"msg":"user 192.168.1.1 logged in","level":"info"}`,
			// level value "info" has no variable parts → preserved
			want: `{"level":"info","msg":"user <ip> logged in"}`,
		},
		{
			name:  "normalizes trace ids in string values",
			input: `{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`,
			want:  `{"trace_id":"<hex>"}`,
		},
		{
			name:  "preserves booleans",
			input: `{"ok":true,"failed":false}`,
			want:  `{"failed":false,"ok":true}`,
		},
		{
			name:  "preserves nulls",
			input: `{"user":null}`,
			want:  `{"user":null}`,
		},
		{
			name:  "nested objects",
			input: `{"req":{"method":"GET","path":"/api/v1/users","latency":45.2}}`,
			want:  `{"req":{"latency":"<num>","method":"GET","path":"<path>"}}`,
		},
		{
			name:  "arrays",
			input: `{"ids":["550e8400-e29b-41d4-a716-446655440000","550e8400-e29b-41d4-a716-446655440001"]}`,
			want:  `{"ids":["<uuid>","<uuid>"]}`,
		},
		{
			name:  "invalid JSON fallback to text",
			input: `{not valid json`,
			want:  normalizeText(`{not valid json`),
		},
		{
			name:  "enum-like strings preserved",
			input: `{"status":"ok","env":"production"}`,
			want:  `{"env":"production","status":"ok"}`,
		},
		{
			name:  "empty object",
			input: `{}`,
			want:  `{}`,
		},
		{
			name:  "integer value normalized",
			input: `{"count":42}`,
			want:  `{"count":"<num>"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeJSON(tt.input)
			if got != tt.want {
				t.Errorf("normalizeJSON(%q)\n got  %q\n want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// truncateUTF8
// ---------------------------------------------------------------------------

func TestTruncateUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		want     string
	}{
		{
			name:     "ascii short",
			input:    "hello",
			maxBytes: 10,
			want:     "hello",
		},
		{
			name:     "ascii exact",
			input:    "hello",
			maxBytes: 5,
			want:     "hello",
		},
		{
			name:     "ascii truncate",
			input:    "hello world",
			maxBytes: 5,
			want:     "hello",
		},
		{
			name:     "multibyte safe - don't split 世界",
			input:    "ab世界cd",
			maxBytes: 5, // "ab" (2) + "世" (3) = 5 but "世界" starts at byte 2
			want:     "ab世",
		},
		{
			name:     "empty string",
			input:    "",
			maxBytes: 10,
			want:     "",
		},
		{
			name:     "maxBytes zero",
			input:    "hello",
			maxBytes: 0,
			want:     "",
		},
		{
			name:     "all multibyte no rune fits",
			input:    "世界",
			maxBytes: 2, // 世 is 3 bytes, can't fit
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateUTF8(tt.input, tt.maxBytes)
			if got != tt.want {
				t.Errorf("truncateUTF8(%q, %d)\n got  %q\n want %q", tt.input, tt.maxBytes, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeTemplate
// ---------------------------------------------------------------------------

func TestNormalizeTemplate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "JSON detected by leading brace",
			input: `{"msg":"connected from 10.0.0.1","code":200}`,
			want:  `{"code":"<num>","msg":"connected from <ip>"}`,
		},
		{
			name:  "plain text",
			input: "user 550e8400-e29b-41d4-a716-446655440000 disconnected",
			want:  "user <uuid> disconnected",
		},
		{
			name:  "empty body",
			input: "",
			want:  "",
		},
		{
			name:  "truncation fires before normalization",
			input: strings.Repeat("x", 510) + " 12345",
			want:  strings.Repeat("x", 500), // truncated to 500, number never seen
		},
		{
			name:  "JSON array treated as text",
			input: `[{"ip":"10.0.0.1"}]`,
			want:  `[{<str>:<str>}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTemplate(tt.input)
			if got != tt.want {
				t.Errorf("normalizeTemplate(%q)\n got  %q\n want %q", tt.input, got, tt.want)
			}
		})
	}
}
