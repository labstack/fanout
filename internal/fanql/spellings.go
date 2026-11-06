package fanql

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var identifierPath = regexp.MustCompile(`^[A-Za-z_][A-Za-z_0-9]*(\.[A-Za-z_][A-Za-z_0-9]*)*$`)
var aliasName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

func durationMS(text string) (float64, bool) {
	text = strings.ToLower(text)
	for _, u := range []struct {
		name  string
		scale float64
	}{{"us", .001}, {"ms", 1}, {"s", 1000}, {"m", 60000}, {"h", 3600000}} {
		if strings.HasSuffix(text, u.name) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(text, u.name), 64)
			ms := n * u.scale
			return ms, err == nil && !math.IsNaN(ms) && !math.IsInf(ms, 0)
		}
	}
	return 0, false
}

func timestamp(text string) bool {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05Z0700", "2006-01-02 15:04:05"} {
		if _, err := time.Parse(layout, text); err == nil {
			return true
		}
	}
	return false
}
