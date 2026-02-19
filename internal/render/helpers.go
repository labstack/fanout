package render

import "strings"

// Format specifies output format
type Format string

const (
	ASCII Format = "ascii"
	HTML  Format = "html"
	Both  Format = "both"
	Data  Format = "data"
)

// Output contains rendered content
type Output struct {
	ASCII string `json:"ascii,omitempty"`
	HTML  string `json:"html,omitempty"`
	Title string `json:"title,omitempty"`
}

// Renderer is the legacy interface for backward compatibility
type Renderer interface {
	Render(format Format) Output
}

// Helper functions

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}

func formatFloat(f float64) string {
	if f == float64(int(f)) {
		return itoa(int(f))
	}
	i := int(f * 100)
	whole := i / 100
	frac := i % 100
	if frac < 0 {
		frac = -frac
	}
	if frac == 0 {
		return itoa(whole)
	}
	if frac%10 == 0 {
		return itoa(whole) + "." + itoa(frac/10)
	}
	fracStr := itoa(frac)
	if frac < 10 {
		fracStr = "0" + fracStr
	}
	return itoa(whole) + "." + fracStr
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var sb strings.Builder
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		sb.WriteByte(byte('0' + i%10))
		i /= 10
	}
	if neg {
		sb.WriteByte('-')
	}
	s := sb.String()
	b := []byte(s)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

func statusVariant(status string) string {
	switch status {
	case "healthy", "success", "ok":
		return "success"
	case "degraded", "warning", "slow":
		return "warning"
	case "unhealthy", "error", "failed":
		return "danger"
	default:
		return "neutral"
	}
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func clamp(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func clampInt(val, min, max int) int {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

// parseNumericValue attempts to extract numeric value from string like "120ms", "45%", "$1.2k"
func parseNumericValue(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	// Remove common suffixes and prefixes
	s = strings.TrimPrefix(s, "$")
	s = strings.TrimSuffix(s, "%")
	s = strings.TrimSuffix(s, "ms")
	s = strings.TrimSuffix(s, "s")
	s = strings.TrimSuffix(s, "k")
	s = strings.TrimSuffix(s, "M")
	s = strings.TrimSuffix(s, "GB")
	s = strings.TrimSuffix(s, "MB")
	s = strings.TrimSuffix(s, "KB")
	s = strings.TrimSpace(s)

	var val float64
	var neg bool
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	var decimal bool
	var decimalPlaces int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			if decimal {
				decimalPlaces++
				val = val + float64(c-'0')/pow10(decimalPlaces)
			} else {
				val = val*10 + float64(c-'0')
			}
		} else if c == '.' && !decimal {
			decimal = true
		} else {
			return 0, false
		}
	}
	if neg {
		val = -val
	}
	return val, true
}

func pow10(n int) float64 {
	result := 1.0
	for i := 0; i < n; i++ {
		result *= 10
	}
	return result
}
