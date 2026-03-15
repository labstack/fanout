package mcp

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimeWindow represents a parsed time range.
type TimeWindow struct {
	Start   time.Time
	End     time.Time
	Minutes int // for backward compat with service layer
}

// parseWindow parses a window string into a TimeWindow.
// Supported formats:
//   - Duration: "5m", "1h", "24h", "7d" (relative to now)
//   - ISO range: "2026-03-14T12:00:00Z/2026-03-14T14:00:00Z"
//   - Empty: defaults to 15m
func parseWindow(w string) (TimeWindow, error) {
	// Empty → default 15m
	if w == "" {
		return windowFromDuration(defWindow * time.Minute), nil
	}

	// ISO range: contains "/"
	if strings.Contains(w, "/") {
		return parseISORange(w)
	}

	// Duration suffixes
	switch {
	case strings.HasSuffix(w, "m"):
		return parseDurationSuffix(w[:len(w)-1], time.Minute)
	case strings.HasSuffix(w, "h"):
		return parseDurationSuffix(w[:len(w)-1], time.Hour)
	case strings.HasSuffix(w, "d"):
		return parseDurationSuffix(w[:len(w)-1], 24*time.Hour)
	default:
		return TimeWindow{}, fmt.Errorf("invalid window %q: must end with m, h, or d, or be an ISO range (start/end)", w)
	}
}

// parseDurationSuffix parses the numeric prefix and multiplies by unit.
func parseDurationSuffix(numStr string, unit time.Duration) (TimeWindow, error) {
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return TimeWindow{}, fmt.Errorf("invalid window %q: %w", numStr, err)
	}
	if n <= 0 {
		return TimeWindow{}, fmt.Errorf("invalid window: duration must be positive, got %d", n)
	}
	return windowFromDuration(time.Duration(n) * unit), nil
}

// parseISORange parses "start/end" where each part is RFC3339.
func parseISORange(w string) (TimeWindow, error) {
	parts := strings.SplitN(w, "/", 2)
	// parts always has 2 elements given SplitN with n=2 and a "/" present,
	// but either part may be empty.
	if parts[0] == "" || parts[1] == "" {
		return TimeWindow{}, fmt.Errorf("invalid ISO range %q: both start and end are required", w)
	}

	start, err := time.Parse(time.RFC3339, parts[0])
	if err != nil {
		return TimeWindow{}, fmt.Errorf("invalid ISO range start %q: %w", parts[0], err)
	}

	end, err := time.Parse(time.RFC3339, parts[1])
	if err != nil {
		return TimeWindow{}, fmt.Errorf("invalid ISO range end %q: %w", parts[1], err)
	}

	if !end.After(start) {
		return TimeWindow{}, fmt.Errorf("invalid ISO range: end %v must be after start %v", end, start)
	}

	minutes := int(end.Sub(start).Minutes())
	return TimeWindow{Start: start, End: end, Minutes: minutes}, nil
}

// windowFromDuration builds a TimeWindow ending at now with the given duration.
func windowFromDuration(d time.Duration) TimeWindow {
	end := time.Now()
	start := end.Add(-d)
	minutes := int(d.Minutes())
	return TimeWindow{Start: start, End: end, Minutes: minutes}
}
