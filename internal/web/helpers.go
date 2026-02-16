package web

import (
	"fmt"
	"strings"
)

// clampMin returns the larger of n and 0.
func clampMin(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// truncateStr truncates s to max characters, appending "..." if truncated.
func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// truncateID truncates an ID (trace, span) to 16 chars + "...".
func truncateID(id string) string {
	if len(id) > 16 {
		return id[:16] + "..."
	}
	return id
}

// fmtScopeVersion prepends "@" to a scope version string, or returns "" if empty.
func fmtScopeVersion(v string) string {
	if v == "" {
		return ""
	}
	return "@" + v
}

// fmtAttrValue formats an attribute value for display.
func fmtAttrValue(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

// fmtCount formats large numbers with k/M suffixes (lowercase k).
func fmtCount(n int64) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// fmtAttrs formats a map of string attributes as "k=v, k2=v2".
func fmtAttrs(attrs map[string]string) string {
	if len(attrs) == 0 {
		return ""
	}
	var parts []string
	for k, v := range attrs {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ", ")
}
