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
