package panel

import (
	"fmt"
	"time"
)

const maxSeriesPoints = 2000

var autoIntervals = []time.Duration{
	10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
	30 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// Cell plots need wider time buckets than lines. Widths are rounded outer panel
// widths, so reserve 160px for body padding, axis labels and rounding error.
// At normal widths target 60–90 columns; narrow panels use fewer to retain 8px
// slots. The extra 20m step keeps a 24h window in that range (72–73 columns).
func autoCellInterval(window time.Duration, widthPx int) time.Duration {
	if widthPx <= 0 {
		widthPx = 640
	}
	points := time.Duration(min(max((widthPx-160)/8, 2), 90))
	intervals := []time.Duration{
		10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute,
		20 * time.Minute, 30 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
	}
	for _, interval := range intervals {
		if window/interval+1 <= points {
			return interval
		}
	}
	return intervals[len(intervals)-1]
}

// AutoInterval picks the finest standard interval, 10 seconds or longer,
// that keeps a series at one point per four pixels or fewer, so a 24-hour
// line stays legible instead of becoming a band.
func AutoInterval(window time.Duration, widthPx int) time.Duration {
	if widthPx <= 0 {
		widthPx = 640
	}
	points := time.Duration(min(max(widthPx/4, 30), maxSeriesPoints))
	for _, interval := range autoIntervals {
		// An unaligned window of N intervals touches N+1 buckets.
		if window/interval+1 <= points {
			return interval
		}
	}
	return autoIntervals[len(autoIntervals)-1]
}

// bucketInterval resolves a query's bucket. A fixed bucket that would exceed
// the point cap for this window widens to the next standard interval.
func bucketInterval(bucket string, window time.Duration, widthPx int) time.Duration {
	switch bucket {
	case "":
		return 0
	case "auto":
		return AutoInterval(window, widthPx)
	}
	interval := buckets[bucket]
	for _, candidate := range autoIntervals {
		if candidate >= interval && window/candidate+1 <= maxSeriesPoints {
			return candidate
		}
	}
	return autoIntervals[len(autoIntervals)-1]
}

func formatInterval(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}
