package panel

import (
	"errors"
	"fmt"
	"time"
)

// resolveWindow turns a dashboard time, optionally overridden by a panel,
// into a concrete UTC range no longer than retention. A range longer than
// retention keeps its end and loses its oldest part rather than failing.
func resolveWindow(t Time, override *PanelTime, now time.Time, maxWindow time.Duration) (time.Time, time.Time, error) {
	var start, end time.Time
	if t.From != nil && t.To != nil {
		start, end = t.From.UTC(), t.To.UTC()
	} else {
		span, ok := ranges[t.Range]
		if !ok {
			return time.Time{}, time.Time{}, fmt.Errorf("unsupported range %q", t.Range)
		}
		end = now.UTC()
		start = end.Add(-span)
	}
	if override != nil {
		if override.Range != "" {
			span, ok := ranges[override.Range]
			if !ok {
				return time.Time{}, time.Time{}, fmt.Errorf("unsupported range %q", override.Range)
			}
			start = end.Add(-span)
		}
		if override.Shift != "" {
			shift, ok := ParseSpan(override.Shift)
			if !ok {
				return time.Time{}, time.Time{}, fmt.Errorf("unsupported shift %q", override.Shift)
			}
			start, end = start.Add(-shift), end.Add(-shift)
		}
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, errors.New("the time range must start before it ends")
	}
	if maxWindow > 0 && end.Sub(start) > maxWindow {
		start = end.Add(-maxWindow)
	}
	return start, end, nil
}
