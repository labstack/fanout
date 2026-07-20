package observability

import "testing"

func TestRedactLogBody(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "URL query value",
			in:   `request failed: https://api.example.test/data?series=gold&api_key=abc123&limit=1`,
			want: `request failed: https://api.example.test/data?series=gold&api_key=[REDACTED]&limit=1`,
		},
		{
			name: "assignment",
			in:   `client_secret="super-secret" token=opaque-value`,
			want: `client_secret=[REDACTED] token=[REDACTED]`,
		},
		{
			name: "authorization",
			in:   `Authorization: Bearer eyJhbGciOi.secret.signature`,
			want: `Authorization: [REDACTED]`,
		},
		{
			name: "ordinary log",
			in:   `worker completed 24 records in 18ms`,
			want: `worker completed 24 records in 18ms`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := redactLogBody(test.in); got != test.want {
				t.Fatalf("redactLogBody() = %q, want %q", got, test.want)
			}
		})
	}
}
