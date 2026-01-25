package render

import (
	"testing"
)

func TestPadRight(t *testing.T) {
	tests := []struct {
		input    string
		n        int
		expected string
	}{
		{"hello", 10, "hello     "},
		{"hello", 5, "hello"},
		{"hello", 3, "hello"},
		{"", 5, "     "},
		{"test", 0, "test"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := padRight(tc.input, tc.n)
			if result != tc.expected {
				t.Errorf("padRight(%q, %d) = %q, want %q", tc.input, tc.n, result, tc.expected)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"hello world", 5, "hell…"},
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello", 1, "…"},
		{"hello", 0, "…"},
		{"", 5, ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := truncate(tc.input, tc.max)
			if result != tc.expected {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.max, result, tc.expected)
			}
		})
	}
}

func TestFormatFloat(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{42.0, "42"},
		{42.5, "42.5"},
		{42.25, "42.25"},
		{0.0, "0"},
		{100.00, "100"},
		{3.14, "3.14"},
		{-5.5, "-5.5"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			result := formatFloat(tc.input)
			if result != tc.expected {
				t.Errorf("formatFloat(%f) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input    int
		expected string
	}{
		{0, "0"},
		{42, "42"},
		{-42, "-42"},
		{12345, "12345"},
		{-1, "-1"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			result := itoa(tc.input)
			if result != tc.expected {
				t.Errorf("itoa(%d) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestStatusVariant(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"healthy", "success"},
		{"success", "success"},
		{"ok", "success"},
		{"degraded", "warning"},
		{"warning", "warning"},
		{"slow", "warning"},
		{"unhealthy", "danger"},
		{"error", "danger"},
		{"failed", "danger"},
		{"unknown", "neutral"},
		{"", "neutral"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := statusVariant(tc.input)
			if result != tc.expected {
				t.Errorf("statusVariant(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestEscapeHTML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"<script>", "&lt;script&gt;"},
		{"a & b", "a &amp; b"},
		{`"quoted"`, "&quot;quoted&quot;"},
		{"<>&\"", "&lt;&gt;&amp;&quot;"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := escapeHTML(tc.input)
			if result != tc.expected {
				t.Errorf("escapeHTML(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		val, min, max float64
		expected      float64
	}{
		{5.0, 0.0, 10.0, 5.0},
		{-5.0, 0.0, 10.0, 0.0},
		{15.0, 0.0, 10.0, 10.0},
		{0.0, 0.0, 10.0, 0.0},
		{10.0, 0.0, 10.0, 10.0},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			result := clamp(tc.val, tc.min, tc.max)
			if result != tc.expected {
				t.Errorf("clamp(%f, %f, %f) = %f, want %f",
					tc.val, tc.min, tc.max, result, tc.expected)
			}
		})
	}
}

func TestClampInt(t *testing.T) {
	tests := []struct {
		val, min, max int
		expected      int
	}{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 10, 0},
		{10, 0, 10, 10},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			result := clampInt(tc.val, tc.min, tc.max)
			if result != tc.expected {
				t.Errorf("clampInt(%d, %d, %d) = %d, want %d",
					tc.val, tc.min, tc.max, result, tc.expected)
			}
		})
	}
}

func TestParseNumericValue(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
		ok       bool
	}{
		{"42", 42, true},
		{"3.14", 3.14, true},
		{"120ms", 120, true},
		{"45%", 45, true},
		{"$1.5", 1.5, true},
		{"-10", -10, true},
		{"100k", 100, true},
		{"5GB", 5, true},
		{"", 0, false},
		{"abc", 0, false},
		{"12.34.56", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, ok := parseNumericValue(tc.input)
			if ok != tc.ok {
				t.Errorf("parseNumericValue(%q) ok = %v, want %v", tc.input, ok, tc.ok)
			}
			if ok && result != tc.expected {
				t.Errorf("parseNumericValue(%q) = %f, want %f", tc.input, result, tc.expected)
			}
		})
	}
}

func TestPow10(t *testing.T) {
	tests := []struct {
		n        int
		expected float64
	}{
		{0, 1},
		{1, 10},
		{2, 100},
		{3, 1000},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			result := pow10(tc.n)
			if result != tc.expected {
				t.Errorf("pow10(%d) = %f, want %f", tc.n, result, tc.expected)
			}
		})
	}
}
