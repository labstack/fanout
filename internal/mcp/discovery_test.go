package mcp

import "testing"

func TestClampInt(t *testing.T) {
	tests := []struct {
		name     string
		v        int
		min      int
		max      int
		def      int
		expected int
	}{
		// Zero returns default
		{"zero returns default", 0, 1, 100, 15, 15},

		// Below min clamps to min
		{"below min", -5, 1, 100, 15, 1},
		{"at min boundary", 1, 1, 100, 15, 1},

		// Above max clamps to max
		{"above max", 500, 1, 100, 15, 100},
		{"at max boundary", 100, 1, 100, 15, 100},

		// Within range returns value
		{"within range", 50, 1, 100, 15, 50},
		{"just above min", 2, 1, 100, 15, 2},
		{"just below max", 99, 1, 100, 15, 99},

		// Window constants
		{"window default", 0, minWindow, maxWindow, defWindow, 15},
		{"window min clamp", 0, minWindow, maxWindow, defWindow, 15},
		{"window valid", 60, minWindow, maxWindow, defWindow, 60},
		{"window max clamp", 9999, minWindow, maxWindow, defWindow, maxWindow},

		// Limit constants
		{"limit default", 0, minLimit, maxLimit, defLimit, 50},
		{"limit valid", 100, minLimit, maxLimit, defLimit, 100},
		{"limit max clamp", 5000, minLimit, maxLimit, defLimit, maxLimit},

		// Granularity constants
		{"granularity default", 0, minGranularity, maxGranularity, defGranularity, 5},
		{"granularity valid", 10, minGranularity, maxGranularity, defGranularity, 10},
		{"granularity max clamp", 120, minGranularity, maxGranularity, defGranularity, maxGranularity},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := clampInt(tc.v, tc.min, tc.max, tc.def)
			if result != tc.expected {
				t.Errorf("clampInt(%d, %d, %d, %d) = %d, want %d",
					tc.v, tc.min, tc.max, tc.def, result, tc.expected)
			}
		})
	}
}

func TestConstants(t *testing.T) {
	// Verify constants are reasonable
	if minWindow < 1 {
		t.Errorf("minWindow = %d, should be >= 1", minWindow)
	}
	if maxWindow < minWindow {
		t.Errorf("maxWindow = %d should be >= minWindow = %d", maxWindow, minWindow)
	}
	if defWindow < minWindow || defWindow > maxWindow {
		t.Errorf("defWindow = %d should be in [%d, %d]", defWindow, minWindow, maxWindow)
	}

	if minLimit < 1 {
		t.Errorf("minLimit = %d, should be >= 1", minLimit)
	}
	if maxLimit < minLimit {
		t.Errorf("maxLimit = %d should be >= minLimit = %d", maxLimit, minLimit)
	}

	if minGranularity < 1 {
		t.Errorf("minGranularity = %d, should be >= 1", minGranularity)
	}
	if maxGranularity < minGranularity {
		t.Errorf("maxGranularity = %d should be >= minGranularity = %d", maxGranularity, minGranularity)
	}
}
