package mcp

// Input validation helpers

// clampInt returns v clamped to [min, max], or def if v is 0.
func clampInt(v, min, max, def int) int {
	if v == 0 {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// Validation constants
const (
	minWindow = 1
	maxWindow = 1440 // 24 hours
	defWindow = 15

	minLimit = 1
	maxLimit = 1000
	defLimit = 50

	minGranularity = 1
	maxGranularity = 60
	defGranularity = 5
)
