package panel

import (
	"strings"
	"testing"
	"time"
)

func TestResolveWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	from, to := now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	week := 7 * 24 * time.Hour
	cases := []struct {
		name       string
		t          Time
		override   *PanelTime
		start, end time.Time
		err        string
	}{
		{"relative", Time{Range: "1h"}, nil, now.Add(-time.Hour), now, ""},
		{"absolute", Time{From: &from, To: &to}, nil, from, to, ""},
		{"panel range clamped to retention", Time{Range: "1h"}, &PanelTime{Range: "30d"}, now.Add(-week), now, ""},
		{"panel shift", Time{Range: "1h"}, &PanelTime{Shift: "1d"}, now.Add(-25 * time.Hour), now.Add(-24 * time.Hour), ""},
		{"from after to", Time{From: &to, To: &from}, nil, time.Time{}, time.Time{}, "start before it ends"},
		{"bad range", Time{Range: "90m"}, nil, time.Time{}, time.Time{}, "unsupported range"},
	}
	for _, tc := range cases {
		start, end, err := resolveWindow(tc.t, tc.override, now, week)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err = %v", tc.name, err)
			}
			continue
		}
		if err != nil || !start.Equal(tc.start) || !end.Equal(tc.end) {
			t.Errorf("%s: got %v – %v (%v), want %v – %v", tc.name, start, end, err, tc.start, tc.end)
		}
	}
}

func TestAutoInterval(t *testing.T) {
	cases := []struct {
		window time.Duration
		width  int
		want   time.Duration
	}{
		{5 * time.Minute, 800, 10 * time.Second},
		{time.Hour, 800, 30 * time.Second},
		{time.Hour, 0, 30 * time.Second},
		{24 * time.Hour, 800, 10 * time.Minute},
		{30 * 24 * time.Hour, 800, 6 * time.Hour},
	}
	for _, tc := range cases {
		if got := AutoInterval(tc.window, tc.width); got != tc.want {
			t.Errorf("AutoInterval(%v, %d) = %v, want %v", tc.window, tc.width, got, tc.want)
		}
	}
	if got := AutoInterval(30*24*time.Hour, 1000000); 30*24*time.Hour/got+1 > maxSeriesPoints {
		t.Errorf("auto interval exceeds the point cap: %v", got)
	}
	if got := bucketInterval("10s", 30*24*time.Hour, 800); time.Duration(30*24)*time.Hour/got > maxSeriesPoints {
		t.Errorf("fixed bucket exceeds the point cap: %v", got)
	}
	if formatInterval(90*time.Second) != "90s" || formatInterval(5*time.Minute) != "5m" || formatInterval(24*time.Hour) != "1d" {
		t.Error("formatInterval")
	}
}

func TestValueJSON(t *testing.T) {
	var v Value
	for text, want := range map[string]Value{`"checkout"`: {Values: []string{"checkout"}}, `"$__all"`: {All: true}, `["a","b"]`: {Values: []string{"a", "b"}}, `[]`: {Values: []string{}}} {
		if err := v.UnmarshalJSON([]byte(text)); err != nil || v.All != want.All || strings.Join(v.Values, ",") != strings.Join(want.Values, ",") {
			t.Errorf("%s → %+v %v", text, v, err)
		}
	}
	if err := v.UnmarshalJSON([]byte(`3`)); err == nil {
		t.Error("number accepted")
	}
}
