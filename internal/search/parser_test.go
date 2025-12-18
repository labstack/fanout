package search

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  *Query
	}{
		{
			name:  "empty",
			input: "",
			want:  &Query{Fields: map[string][]string{}},
		},
		{
			name:  "single term",
			input: "error",
			want:  &Query{Terms: []string{"error"}, Fields: map[string][]string{}},
		},
		{
			name:  "multiple terms",
			input: "connection timeout",
			want:  &Query{Terms: []string{"connection", "timeout"}, Fields: map[string][]string{}},
		},
		{
			name:  "quoted phrase",
			input: `"connection timeout"`,
			want:  &Query{Terms: []string{"connection timeout"}, Fields: map[string][]string{}},
		},
		{
			name:  "exclude term",
			input: "error -retry",
			want:  &Query{Terms: []string{"error"}, Exclude: []string{"retry"}, Fields: map[string][]string{}},
		},
		{
			name:  "service filter",
			input: "service:cart",
			want:  &Query{Fields: map[string][]string{"service": {"cart"}}},
		},
		{
			name:  "multiple service values",
			input: "service:cart,checkout",
			want:  &Query{Fields: map[string][]string{"service": {"cart", "checkout"}}},
		},
		{
			name:  "severity filter",
			input: "severity:ERROR",
			want:  &Query{Fields: map[string][]string{"severity": {"ERROR"}}},
		},
		{
			name:  "combined query",
			input: `service:cart severity:ERROR "connection timeout" -retry`,
			want: &Query{
				Terms:   []string{"connection timeout"},
				Exclude: []string{"retry"},
				Fields: map[string][]string{
					"service":  {"cart"},
					"severity": {"ERROR"},
				},
			},
		},
		{
			name:  "case preserved in values",
			input: "severity:ERROR,WARN",
			want:  &Query{Fields: map[string][]string{"severity": {"ERROR", "WARN"}}},
		},
		{
			name:  "field name lowercased",
			input: "SERVICE:cart",
			want:  &Query{Fields: map[string][]string{"service": {"cart"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.input)
			if !reflect.DeepEqual(got.Terms, tt.want.Terms) {
				t.Errorf("Terms = %v, want %v", got.Terms, tt.want.Terms)
			}
			if !reflect.DeepEqual(got.Exclude, tt.want.Exclude) {
				t.Errorf("Exclude = %v, want %v", got.Exclude, tt.want.Exclude)
			}
			if !reflect.DeepEqual(got.Fields, tt.want.Fields) {
				t.Errorf("Fields = %v, want %v", got.Fields, tt.want.Fields)
			}
		})
	}
}

func TestQuery_IsEmpty(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"error", false},
		{"-exclude", false},
		{"service:cart", false},
	}

	for _, tt := range tests {
		q := Parse(tt.input)
		if got := q.IsEmpty(); got != tt.want {
			t.Errorf("Parse(%q).IsEmpty() = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestQuery_Service(t *testing.T) {
	q := Parse("service:cart,checkout error")
	if got := q.Service(); !reflect.DeepEqual(got, []string{"cart", "checkout"}) {
		t.Errorf("Service() = %v, want [cart checkout]", got)
	}

	q2 := Parse("error")
	if got := q2.Service(); got != nil {
		t.Errorf("Service() = %v, want nil", got)
	}
}

func TestQuery_Severity(t *testing.T) {
	q := Parse("severity:ERROR,WARN")
	if got := q.Severity(); !reflect.DeepEqual(got, []string{"ERROR", "WARN"}) {
		t.Errorf("Severity() = %v, want [ERROR WARN]", got)
	}
}
