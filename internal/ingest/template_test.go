package ingest

import (
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
			want:  "at <ts> the event fired",
		},
		{
			name:  "timestamp with Z",
			input: "logged at 2023-06-01T00:00:00Z",
			want:  "logged at <ts>",
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
			want:  "user <uuid> from <ip> at <ts>",
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
