package mcp

import (
	"testing"
)

func TestParseWindow(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		checkFn func(t *testing.T, tw TimeWindow)
	}{
		{"5m duration", "5m", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 5 {
				t.Errorf("Minutes = %d, want 5", tw.Minutes)
			}
			dur := tw.End.Sub(tw.Start).Minutes()
			if dur < 4.9 || dur > 5.1 {
				t.Errorf("window duration not ~5m: %v", tw.End.Sub(tw.Start))
			}
		}},
		{"1h duration", "1h", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 60 {
				t.Errorf("Minutes = %d, want 60", tw.Minutes)
			}
		}},
		{"24h duration", "24h", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 1440 {
				t.Errorf("Minutes = %d, want 1440", tw.Minutes)
			}
		}},
		{"7d duration", "7d", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 10080 {
				t.Errorf("Minutes = %d, want 10080", tw.Minutes)
			}
		}},
		{"ISO range", "2026-03-14T12:00:00Z/2026-03-14T14:00:00Z", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 120 {
				t.Errorf("Minutes = %d, want 120", tw.Minutes)
			}
			if tw.Start.Hour() != 12 {
				t.Errorf("Start hour = %d, want 12", tw.Start.Hour())
			}
			if tw.End.Hour() != 14 {
				t.Errorf("End hour = %d, want 14", tw.End.Hour())
			}
		}},
		{"empty default", "", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 15 {
				t.Errorf("Minutes = %d, want 15", tw.Minutes)
			}
		}},
		{"invalid", "invalid", true, nil},
		{"negative number", "-5m", true, nil},
		{"zero duration", "0m", true, nil},
		// Additional edge cases
		{"30m duration", "30m", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 30 {
				t.Errorf("Minutes = %d, want 30", tw.Minutes)
			}
		}},
		{"2h duration", "2h", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 120 {
				t.Errorf("Minutes = %d, want 120", tw.Minutes)
			}
		}},
		{"1d duration", "1d", false, func(t *testing.T, tw TimeWindow) {
			if tw.Minutes != 1440 {
				t.Errorf("Minutes = %d, want 1440", tw.Minutes)
			}
		}},
		{"zero hours", "0h", true, nil},
		{"zero days", "0d", true, nil},
		{"just slash", "/", true, nil},
		{"ISO range missing end", "2026-03-14T12:00:00Z/", true, nil},
		{"ISO range missing start", "/2026-03-14T14:00:00Z", true, nil},
		{"ISO range bad start", "not-a-date/2026-03-14T14:00:00Z", true, nil},
		{"ISO range bad end", "2026-03-14T12:00:00Z/not-a-date", true, nil},
		{"ISO range end before start", "2026-03-14T14:00:00Z/2026-03-14T12:00:00Z", true, nil},
		{"bare number no suffix", "60", true, nil},
		{"letter prefix", "xm", true, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tw, err := parseWindow(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseWindow(%q) expected error, got nil (result: %+v)", tc.input, tw)
				}
				return
			}
			if err != nil {
				t.Errorf("parseWindow(%q) unexpected error: %v", tc.input, err)
				return
			}
			if tw.Start.IsZero() {
				t.Errorf("parseWindow(%q) Start is zero", tc.input)
			}
			if tw.End.IsZero() {
				t.Errorf("parseWindow(%q) End is zero", tc.input)
			}
			if !tw.End.After(tw.Start) {
				t.Errorf("parseWindow(%q) End %v is not after Start %v", tc.input, tw.End, tw.Start)
			}
			if tc.checkFn != nil {
				tc.checkFn(t, tw)
			}
		})
	}
}
